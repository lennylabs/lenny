# t23.py: rows curl cannot produce or that an off-by-one in t20 missed.
import http.client,ssl,json
ctx=ssl.create_default_context(cafile='/s/bundle.pem')
def go(label,m,p,body=None,h=None,host='api.github.com',exp=''):
    c=http.client.HTTPSConnection('127.0.0.1',3128,context=ctx,timeout=12); c.set_tunnel(host,443)
    hd={'Authorization':'token lenny-injected','Content-Type':'application/json'}
    hd.update(h or {})
    try:
        c.request(m,p,body=body,headers=hd); r=c.getresponse(); b=r.read(160)
        print(f'{label:46s} exp={exp} http={r.status} {b[:110]!r}')
    except Exception as e: print(label,'error',repr(e)[:90])
    finally: c.close()
Q='query{viewer{login}}'
def jn(depth): return '{"query":"%s","variables":%s}'%(Q,'{"a":'*(depth-1)+'1'+'}'*(depth-1))
for d in(31,32,33,34): go(f'JSON containers total {d}','POST','/graphql',jn(d),exp='fwd' if d<=32 else 'ref')
go('POST bare ? on a write','POST','/repos/acme/widgets/issues?','{}',exp='ref')
go('GET bare ?','GET','/repos/acme/widgets/issues?',exp='fwd')
go('lower-case put','put','/repos/acme/widgets/pulls/7/merge','{}',exp='ref')
go('custom verb on a denied path','FOO','/repos/acme/widgets/pulls/7/merge','{}',exp='ref')
go('TRACE on a denied path','TRACE','/repos/acme/widgets/pulls/7/merge',exp='ref')
go('OPTIONS on a denied path','OPTIONS','/repos/acme/widgets/pulls/7/merge',exp='fwd')
go('CONNECT inside the tunnel','CONNECT','/repos/acme/widgets',exp='ref')
