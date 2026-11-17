import re,statistics as st,collections
rows=collections.defaultdict(list)
cur=None
for l in open('raw/i1-hardlimit.txt'):
    m=re.match(r'(\S+) up=(\S+) go=(\S+) ramp=(\d+) rep=(\d+) state=\[(.*?)\]',l)
    if m: cur=m.groups(); continue
    m=re.search(r'up_total=(\d+) live_peak=(\d+) live_at_last_stats=(\d+) fail_at_last_stats=(\d+)',l)
    if m and cur:
        rows[(cur[0],cur[2],cur[3])].append((int(m[1]),int(m[3]),int(m[4]),'oom=true' in cur[5]))
for k,v in rows.items():
    print(k,'peak streams',[x[0] for x in v],'median',st.median(x[0] for x in v),'fail before kill',sum(x[2] for x in v),'oom',sum(x[3] for x in v),'of',len(v))
