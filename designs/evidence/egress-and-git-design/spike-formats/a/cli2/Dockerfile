FROM python:3.13-slim
RUN python -c "import urllib.request,zipfile; urllib.request.urlretrieve('https://awscli.amazonaws.com/awscli-exe-linux-aarch64.zip','/tmp/a.zip'); zipfile.ZipFile('/tmp/a.zip').extractall('/tmp')" \
 && chmod -R +x /tmp/aws && /tmp/aws/install && rm -rf /tmp/aws /tmp/a.zip && aws --version
