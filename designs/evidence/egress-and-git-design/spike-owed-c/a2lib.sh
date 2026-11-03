O=$(cd "$(dirname "$0")" && pwd); cd "$O"
NL=(docker run --name oc-nl --network container:oc-na --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/run/trust:/s:ro -v $O/certs:/certs:ro -v $O/run/netagent:/run/netagent:ro alpine:3.22 /b/naload)
memr(){ # prints current/anon/sock/kernel in MiB, peak, and Go figures
  docker exec oc-na sh -c 'c=$(cat /sys/fs/cgroup/memory.current); p=$(cat /sys/fs/cgroup/memory.peak); a=$(grep "^anon " /sys/fs/cgroup/memory.stat|cut -d" " -f2); s=$(grep "^sock " /sys/fs/cgroup/memory.stat|cut -d" " -f2); k=$(grep "^kernel " /sys/fs/cgroup/memory.stat|cut -d" " -f2); echo $c $p $a $s $k $(wget -qO- http://127.0.0.1:9100/stats)'
}
cpuu(){ docker exec oc-na sh -c 'grep usage_usec /sys/fs/cgroup/cpu.stat | cut -d" " -f2'; }
thr(){ docker exec oc-na sh -c 'grep -E "nr_throttled|throttled_usec" /sys/fs/cgroup/cpu.stat | tr "\n" " "'; }
