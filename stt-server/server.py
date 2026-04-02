#!/usr/bin/env python3
"""
Minimal STT server for voicedaemon + studio content pipeline.

Two doors into the same room:
  1. WebSocket  — voicedaemon live streaming (VAD-driven, low latency)
  2. HTTP POST  — studio batch transcription (whole files, word timestamps)

WebSocket protocol (voicedaemon rtc.Client):
  Client → Server: session.update, input_audio_buffer.append
  Server → Client: session.created, session.updated,
                   input_audio_buffer.speech_started/stopped,
                   input_audio_buffer.committed,
                   conversation.item.input_audio_transcription.completed

HTTP endpoint:
  POST /v1/audio/transcriptions  — multipart WAV upload, returns word timestamps
  Response matches OpenAI verbose_json format for zero-change consumer migration.

Dependencies: websockets, aiohttp, faster-whisper, silero-vad, numpy
"""

import asyncio
import base64
import io
import json
import logging
import os
import time
import wave

import numpy as np
import websockets
from aiohttp import web
from faster_whisper import WhisperModel
from silero_vad import load_silero_vad

log = logging.getLogger("stt")
logging.basicConfig(
    level=logging.DEBUG if os.getenv("STT_DEBUG") else logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
)

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
HOST = os.getenv("STT_HOST", "0.0.0.0")
WS_PORT = int(os.getenv("STT_PORT", "2700"))
HTTP_PORT = int(os.getenv("STT_HTTP_PORT", "8000"))
MODEL = os.getenv("STT_MODEL", "deepdml/faster-whisper-large-v3-turbo-ct2")
DEVICE = os.getenv("STT_DEVICE", "cuda")
COMPUTE = os.getenv("STT_COMPUTE", "float16")
LANGUAGE = os.getenv("STT_LANGUAGE", "en")

INPUT_RATE = 24000   # voicedaemon sends 24kHz int16 PCM
TARGET_RATE = 16000  # Whisper and Silero expect 16kHz
VAD_CHUNK = 512      # Silero v5: 512 samples @ 16kHz = 32ms

# ---------------------------------------------------------------------------
# Model loading (once, at startup)
# ---------------------------------------------------------------------------
log.info("Loading Silero VAD (ONNX, CPU)...")
vad_model = load_silero_vad(onnx=True)
log.info("Silero VAD ready")

log.info("Loading Whisper: %s (%s/%s)...", MODEL, DEVICE, COMPUTE)
whisper_model = WhisperModel(MODEL, device=DEVICE, compute_type=COMPUTE)
log.info("Whisper ready")


# ---------------------------------------------------------------------------
# Audio resampling: 24kHz → 16kHz (factor 3:2)
# ---------------------------------------------------------------------------
def resample_24k_16k(samples: np.ndarray) -> np.ndarray:
    """Linear-interpolation resample. Deterministic, no FFT."""
    n_out = (len(samples) * 2) // 3
    if n_out == 0:
        return np.array([], dtype=np.float32)
    idx = np.arange(n_out, dtype=np.float64) * 1.5
    lo = idx.astype(np.intp)
    hi = np.minimum(lo + 1, len(samples) - 1)
    frac = (idx - lo).astype(np.float32)
    return samples[lo] * (1.0 - frac) + samples[hi] * frac


# ---------------------------------------------------------------------------
# Shared transcription helpers
# ---------------------------------------------------------------------------
def _transcribe_live(audio: np.ndarray, language: str) -> tuple[str, float]:
    """Live path: text only, no word timestamps."""
    t0 = time.monotonic()
    lang = None if language == "auto" else language
    segments, _ = whisper_model.transcribe(audio, language=lang, vad_filter=False)
    text = " ".join(s.text for s in segments).strip()
    return text, time.monotonic() - t0


def _transcribe_batch(audio: np.ndarray, language: str) -> dict:
    """Batch path: full transcript with word-level timestamps."""
    t0 = time.monotonic()
    lang = None if language == "auto" else language
    segments, info = whisper_model.transcribe(
        audio, language=lang, vad_filter=True, word_timestamps=True
    )
    words = []
    text_parts = []
    for segment in segments:
        text_parts.append(segment.text)
        if segment.words:
            for w in segment.words:
                words.append({
                    "word": w.word.strip(),
                    "start": round(w.start, 3),
                    "end": round(w.end, 3),
                })
    text = " ".join(text_parts).strip()
    duration = len(audio) / TARGET_RATE
    dt = time.monotonic() - t0
    rtx = duration / dt if dt > 0 else 0
    log.info("Batch (%.2fs audio, %.2fs decode, %.0fx RT): %s",
             duration, dt, rtx, text[:80] + ("..." if len(text) > 80 else ""))
    return {
        "text": text,
        "duration": round(duration, 2),
        "language": info.language if info.language else lang or LANGUAGE,
        "words": words,
    }


def _decode_wav(data: bytes) -> np.ndarray:
    """Decode WAV bytes to float32 numpy array at 16kHz."""
    with wave.open(io.BytesIO(data), "rb") as wf:
        sr = wf.getframerate()
        ch = wf.getnchannels()
        sw = wf.getsampwidth()
        raw = wf.readframes(wf.getnframes())

    if sw == 2:
        samples = np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0
    elif sw == 4:
        samples = np.frombuffer(raw, dtype=np.int32).astype(np.float32) / 2147483648.0
    else:
        raise ValueError(f"Unsupported sample width: {sw}")

    # Mix to mono if stereo
    if ch > 1:
        samples = samples.reshape(-1, ch).mean(axis=1)

    # Resample to 16kHz if needed
    if sr != TARGET_RATE:
        n_out = int(len(samples) * TARGET_RATE / sr)
        idx = np.linspace(0, len(samples) - 1, n_out)
        samples = np.interp(idx, np.arange(len(samples)), samples).astype(np.float32)

    return samples


# ===========================================================================
# Door 1: WebSocket — voicedaemon live streaming
# ===========================================================================
class Session:
    def __init__(self, ws):
        self.ws = ws
        self.threshold = 0.9
        self.silence_ms = 550
        self.language = LANGUAGE
        # VAD state
        self.speaking = False
        self.speech_buf: list[np.ndarray] = []
        self.silence_chunks = 0
        self.remainder = np.array([], dtype=np.float32)

    async def send(self, event: dict):
        await self.ws.send(json.dumps(event))

    async def handle(self, raw: str):
        ev = json.loads(raw)
        t = ev.get("type", "")
        if t == "session.update":
            await self._on_session_update(ev)
        elif t == "input_audio_buffer.append":
            await self._on_audio(ev)

    async def _on_session_update(self, ev):
        s = ev.get("session", {})
        td = s.get("turn_detection", {})
        self.threshold = td.get("threshold", self.threshold)
        self.silence_ms = td.get("silence_duration_ms", self.silence_ms)
        tr = s.get("input_audio_transcription", {})
        self.language = tr.get("language", self.language) or LANGUAGE
        log.info(
            "Config: vad=%.2f silence=%dms lang=%s",
            self.threshold, self.silence_ms, self.language,
        )
        await self.send({"type": "session.updated"})

    async def _on_audio(self, ev):
        pcm = np.frombuffer(base64.b64decode(ev["audio"]), dtype=np.int16)
        f32 = pcm.astype(np.float32) / 32768.0
        f16k = resample_24k_16k(f32)

        if len(self.remainder) > 0:
            f16k = np.concatenate([self.remainder, f16k])

        pos = 0
        while pos + VAD_CHUNK <= len(f16k):
            chunk = f16k[pos : pos + VAD_CHUNK]
            pos += VAD_CHUNK

            prob = float(vad_model(chunk, TARGET_RATE))

            if prob >= self.threshold:
                if not self.speaking:
                    self.speaking = True
                    self.silence_chunks = 0
                    log.debug("Speech started (p=%.3f)", prob)
                    await self.send({"type": "input_audio_buffer.speech_started"})
                self.speech_buf.append(chunk)
                self.silence_chunks = 0
            elif self.speaking:
                self.speech_buf.append(chunk)
                self.silence_chunks += 1
                elapsed_ms = self.silence_chunks * (VAD_CHUNK * 1000 // TARGET_RATE)
                if elapsed_ms >= self.silence_ms:
                    await self._end_speech()

        self.remainder = f16k[pos:]

    async def _end_speech(self):
        self.speaking = False
        self.silence_chunks = 0
        await self.send({"type": "input_audio_buffer.speech_stopped"})

        if not self.speech_buf:
            vad_model.reset_states()
            return

        audio = np.concatenate(self.speech_buf)
        self.speech_buf = []
        vad_model.reset_states()

        if len(audio) < TARGET_RATE // 10:
            return

        loop = asyncio.get_running_loop()
        text, dt = await loop.run_in_executor(
            None, _transcribe_live, audio, self.language
        )

        if text:
            rtx = len(audio) / TARGET_RATE / dt if dt > 0 else 0
            log.info("Transcript (%.2fs, %.0fx RT): %s", dt, rtx, text)
            await self.send({"type": "input_audio_buffer.committed"})
            await self.send({
                "type": "conversation.item.input_audio_transcription.completed",
                "transcript": text,
            })


async def ws_handler(ws):
    addr = ws.remote_address
    log.info("WS connected: %s", addr)
    session = Session(ws)
    await session.send({"type": "session.created"})
    try:
        async for msg in ws:
            await session.handle(msg)
    except websockets.ConnectionClosed:
        pass
    finally:
        vad_model.reset_states()
        log.info("WS disconnected: %s", addr)


# ===========================================================================
# Door 2: HTTP POST — studio batch transcription
# ===========================================================================
async def http_transcribe(request: web.Request) -> web.Response:
    """POST /v1/audio/transcriptions — OpenAI-compatible verbose_json."""
    reader = await request.multipart()
    audio_data = None
    language = LANGUAGE

    async for part in reader:
        if part.name == "file":
            audio_data = await part.read()
        elif part.name == "language":
            language = (await part.text()).strip() or LANGUAGE

    if audio_data is None:
        return web.json_response(
            {"error": "missing 'file' field"}, status=400
        )

    try:
        audio = _decode_wav(audio_data)
    except Exception as e:
        return web.json_response(
            {"error": f"audio decode failed: {e}"}, status=400
        )

    log.info("Batch request: %.1fs audio, lang=%s", len(audio) / TARGET_RATE, language)
    loop = asyncio.get_running_loop()
    result = await loop.run_in_executor(None, _transcribe_batch, audio, language)
    return web.json_response(result)


async def http_health(_request: web.Request) -> web.Response:
    return web.json_response({"status": "ok", "model": MODEL})


def create_http_app() -> web.Application:
    app = web.Application(client_max_size=200 * 1024 * 1024)  # 200MB max upload
    app.router.add_post("/v1/audio/transcriptions", http_transcribe)
    app.router.add_get("/health", http_health)
    return app


# ===========================================================================
# Main — both servers on the same event loop
# ===========================================================================
async def main():
    # Start WebSocket server
    log.info("WebSocket server on %s:%d", HOST, WS_PORT)
    ws_server = await websockets.serve(ws_handler, HOST, WS_PORT, max_size=1 << 20)

    # Start HTTP server
    http_app = create_http_app()
    runner = web.AppRunner(http_app)
    await runner.setup()
    site = web.TCPSite(runner, HOST, HTTP_PORT)
    await site.start()
    log.info("HTTP server on %s:%d", HOST, HTTP_PORT)

    log.info("Ready — both doors open")
    await asyncio.Future()


if __name__ == "__main__":
    asyncio.run(main())
