# ptyrun.py <cmd>: runs a command under a terminal and answers each prompt with Enter, so gh takes its interactive path.
import os,pty,select,sys,time
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('GH_PROMPT_DISABLED',None)
    os.environ['TERM']='xterm'
    os.execvp('sh',['sh','-c',sys.argv[1]])
buf=b'';t0=time.time();last=time.time();sent=0
while time.time()-t0<40:
    r,_,_=select.select([fd],[],[],1)
    if r:
        try: d=os.read(fd,4096)
        except OSError: break
        if not d: break
        buf+=d; last=time.time()
    elif time.time()-last>2 and sent<6:
        os.write(fd,b'\r'); sent+=1; last=time.time()
_,st=os.waitpid(pid,os.WNOHANG)
import re
sys.stdout.write(re.sub(rb'\x1b\[[0-9;?]*[a-zA-Z]',b'',buf).decode('utf8','replace')[-900:])
sys.exit(os.waitstatus_to_exitcode(st) if _ else 124)
