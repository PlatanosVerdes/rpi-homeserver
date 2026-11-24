# RawLoadrr, the uploader of the RaW Suite (codeberg.org/RawSmoke/Singularity), alone: none of the
# rest of the suite. Pinned to one reviewed commit; moving RAWLOADRR_COMMIT means reading the diff.
ARG PYTHON_IMAGE=set-in-versions-env

FROM python:${PYTHON_IMAGE} AS source
ARG RAWLOADRR_COMMIT
RUN apt-get update && apt-get install -y --no-install-recommends git \
    && git init -q /src && cd /src \
    && git remote add origin https://codeberg.org/RawSmoke/Singularity.git \
    && git fetch -q --depth 1 origin "${RAWLOADRR_COMMIT}" \
    && git checkout -q FETCH_HEAD \
    && rm -rf RawLoadrr/data/config.py

FROM python:${PYTHON_IMAGE}
# ffmpeg takes the screenshots, mediainfo describes the file, mktorrent hashes it far faster than
# the pure-Python default. No mono: BDInfo is only for Blu-ray folders.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg mediainfo mktorrent \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=source /src/RawLoadrr/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY --from=source /src/RawLoadrr/ .
ENV HOME=/tmp PYTHONDONTWRITEBYTECODE=1
# Lowest CPU and disk priority: hashing reads the whole film off the USB disks.
ENTRYPOINT ["nice", "-n", "19", "ionice", "-c3", "python3", "/app/upload.py"]
