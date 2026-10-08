FROM python:3.12-slim-bookworm
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates openssl tcpdump tshark libstdc++6 \
    && rm -rf /var/lib/apt/lists/*
COPY claude /opt/claude
RUN echo 'a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3  /opt/claude' | sha256sum -c - \
    && chmod 755 /opt/claude
COPY lab.py validation.py analyze.py transport_lab.py proxy_lab.py /opt/
WORKDIR /work
# Run only with --network none and the local api.anthropic.com host mapping.
ENTRYPOINT ["python3", "/opt/validation.py"]
