FROM python:3.13-slim
RUN pip install --no-cache-dir boto3 google-auth requests azure-identity azure-core PyJWT cryptography && pip freeze > /versions.txt
WORKDIR /app
