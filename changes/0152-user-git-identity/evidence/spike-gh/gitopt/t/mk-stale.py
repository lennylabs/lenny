import json
v=json.load(open('/t/vcs5.json')); json.dump([h for h in v if h['host']!='github.com'],open('/t/vcs5-stale.json','w'),indent=1)
