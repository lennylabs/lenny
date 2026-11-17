import sys
b={l.split()[0]:l.split()[1:] for l in sys.argv[1].splitlines() if l.strip()}; a={l.split()[0]:l.split()[1:] for l in sys.argv[2].splitlines() if l.strip()}; s=float(sys.argv[3])
for k in a:
    if k in b: print(f"   {k}: {(int(a[k][0])-int(b[k][0]))/1e6/s*1000:.0f}m CPU, memory {int(a[k][1])/2**20:.0f} MiB, peak {int(a[k][2])/2**20:.0f} MiB")
