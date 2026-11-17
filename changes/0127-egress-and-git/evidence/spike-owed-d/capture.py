import socket,threading
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); s.bind(("0.0.0.0",3128)); s.listen(16)
def h(c):
    c.settimeout(2)
    try: b=c.recv(4096)
    except Exception: b=b""
    print("len",len(b),"hex",b[:48].hex(),flush=True)
    if b"Proxy-Authorization" in b or b"pod-a" in b: print("PLAINTEXT_CRED",flush=True)
    c.close()
while True:
    c,_=s.accept(); threading.Thread(target=h,args=(c,)).start()
