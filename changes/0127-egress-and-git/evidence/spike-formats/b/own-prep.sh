# Run as root: create netrc files owned by root, mode 0644, for uid 1000 to read.
mkdir -p /home/u /cfg; chown 1000:1000 /home/u
echo 'machine fake.test login alice password s3cret' > /home/u/.netrc; chmod 644 /home/u/.netrc
cp /home/u/.netrc /cfg/netrc; chmod 644 /cfg/netrc; chown root:root /home/u/.netrc /cfg/netrc
ls -ln /home/u/.netrc /cfg/netrc
