FROM python:3.13-alpine

WORKDIR /app
COPY main.py media.py host.py ./

# Standard library only, on purpose: no pip install, no wheel to rebuild on ARM.
EXPOSE 9110
CMD ["python3", "-u", "main.py"]
