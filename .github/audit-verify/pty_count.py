import os, pty, sys, time, select, tempfile
def run(binary, child):
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp("bash", ["bash", "--norc", "--noprofile", "-c", f'for i in 1 2 3; do {binary} exec -- {child}; echo "after $i rc=$?"; done; echo loop-done'])
    time.sleep(1.2)
    os.write(fd, b"\x03")
    out = b""; end = time.time() + 20
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.3)
        if r:
            try: chunk = os.read(fd, 4096)
            except OSError: break
            if not chunk: break
            out += chunk
        try:
            if os.waitpid(pid, os.WNOHANG)[0]: break
        except ChildProcessError: break
    return [l for l in out.decode(errors="replace").replace("\r","").splitlines() if l.strip()]
d = tempfile.mkdtemp(); cnt = os.path.join(d, "n")
pychild = f"""python3 -c 'import signal,time,os
n=[0]
def h(s,f): n[0]+=1
signal.signal(signal.SIGINT,h)
t=time.time()
while time.time()-t<2.5: time.sleep(0.05)
open("{cnt}","a").write(str(n[0])+"\\n")'"""
for b in sys.argv[1:]:
    print(os.path.basename(b), "untrapped sleep:", run(b, "sleep 4")[:3])
    open(cnt, "w").close()
    run(b, pychild)
    print(os.path.basename(b), "python child SIGINT deliveries per Ctrl+C (first iteration):", open(cnt).read().split()[:1])
