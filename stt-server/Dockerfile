FROM nvidia/cuda:12.1.0-runtime-ubuntu22.04

RUN apt-get update && \
    apt-get install -y --no-install-recommends python3 python3-pip git && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Torch CPU-only (for Silero VAD — no GPU needed for VAD)
RUN pip3 install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cpu

COPY requirements.txt .
RUN pip3 install --no-cache-dir -r requirements.txt

# Pre-download Silero VAD model into the image
RUN python3 -c "import torch; torch.hub.load('snakers4/silero-vad', 'silero_vad', trust_repo=True)"

COPY server.py .

EXPOSE 2700
CMD ["python3", "server.py"]
