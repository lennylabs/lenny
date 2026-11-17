# t22b.py: the head deadline and a bare LF.
import socket,time
def go(label,data):
    s=socket.create_connection(('127.0.0.1',3128)); s.settimeout(25); s.sendall(data); t=time.time()
    try: r=s.recv(200)
    except Exception as e: r=repr(e).encode()
    print(label,'answer',r[:60],'after',round(time.time()-t,1),'s')
go('partial head',b'GET http://other.example.test/ HTTP/1.1\r\nHost: other.example.test\r\n')
go('bare LF head',b'GET http://other.example.test/ HTTP/1.1\nHost: other.example.test\n\n')
