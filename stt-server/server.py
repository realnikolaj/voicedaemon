#!/usr/bin/env python3
"""
Minimal STT WebSocket server for voicedaemon.

Speaks the OpenAI Realtime API subset that voicedaemon's rtc.Client
already understands. No framework, no compatibility layer beyond the
six message types the client uses.

Protocol (client → server):
  session.update              — VAD config, language, model
  input_audio_buffer.append   — base64 int16 PCM @ 24kHz

Protocol (server → client):
  session.created / session.updated
  input_audio_buffer.speech_started / speech_stopped
  input_audio_buffer.committed
  conversation.item.input_audio_transcription.completed

Dependencies: websockets, faster-whisper, torch (CPU), numpy
"""

import asyncio
import base64
import json
import logging
import os
import time

import numpy as np
import torch
import websockets
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
PORT = int(os.getenv("STT_PORT", "2700"))
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
# Transcription (runs in thread pool to avoid blocking the event loop)
# ---------------------------------------------------------------------------
def _transcribe(audio: np.ndarray, language: str) -> tuple[str, float]:
    t0 = time.monotonic()
    lang = None if language == "auto" else language
    segments, _ = whisper_model.transcribe(audio, language=lang, vad_filter=False)
    text = " ".join(s.text for s in segments).strip()
    return text, time.monotonic() - t0


# ---------------------------------------------------------------------------
# Session: one WebSocket connection, streaming VAD + transcription
# ---------------------------------------------------------------------------
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

    # -- session.update ------------------------------------------------
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

    # -- audio processing + VAD ----------------------------------------
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

            prob = float(vad_model(torch.from_numpy(chunk), TARGET_RATE))

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

    # -- speech end → transcribe --------------------------------------
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

        # Skip utterances shorter than 100ms
        if len(audio) < TARGET_RATE // 10:
            return

        loop = asyncio.get_running_loop()
        text, dt = await loop.run_in_executor(
            None, _transcribe, audio, self.language
        )

        if text:
            rtx = len(audio) / TARGET_RATE / dt if dt > 0 else 0
            log.info("Transcript (%.2fs, %.0fx RT): %s", dt, rtx, text)
            await self.send({"type": "input_audio_buffer.committed"})
            await self.send({
                "type": "conversation.item.input_audio_transcription.completed",
                "transcript": text,
            })


# ---------------------------------------------------------------------------
# WebSocket handler
# ---------------------------------------------------------------------------
async def handler(ws):
    addr = ws.remote_address
    log.info("Connected: %s", addr)
    session = Session(ws)
    await session.send({"type": "session.created"})
    try:
        async for msg in ws:
            await session.handle(msg)
    except websockets.ConnectionClosed:
        pass
    finally:
        vad_model.reset_states()
        log.info("Disconnected: %s", addr)


async def main():
    log.info("STT server on %s:%d", HOST, PORT)
    async with websockets.serve(handler, HOST, PORT, max_size=1 << 20):
        log.info("Ready — waiting for connections")
        await asyncio.Future()


if __name__ == "__main__":
    asyncio.run(main())
