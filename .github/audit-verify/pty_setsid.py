# Ctrl+C in a terminal while the child has moved to its own session (setsid):
# count the SIGINTs the child receives. The terminal cannot reach it, so only
# env-vault's forwarding can.
import os, pty, select, sys, tempfile, time

def run(binary):
    d = tempfile.mkdtemp()
    cnt = os.path.join(d, "n")
    child = ("import os,signal,time\n"
             "os.setsid()\n"
             "n=[0]\n"
             "signal.signal(signal.SIGINT, lambda s,f: n.__setitem__(0, n[0]+1))\n"
             "t=time.time()\n"
             "while time.time()-t<2.5: time.sleep(0.05)\n"
             f"open({cnt!r},'w').write(str(n[0]))\n")
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp(binary, [binary, "exec", "--", "python3", "-c", child])
    time.sleep(1.2)
    os.write(fd, b"\x03")
    end = time.time() + 10
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.2)
        if r:
            try:
                if not os.read(fd, 4096):
                    break
            except OSError:
                break
        try:
            if os.waitpid(pid, os.WNOHANG)[0]:
                break
        except ChildProcessError:
            break
    time.sleep(0.5)
    got = open(cnt).read() if os.path.exists(cnt) else "none"
    return f"{os.path.basename(binary)} setsid child SIGINT count per Ctrl+C: {got}"

for b in sys.argv[1:]:
    print(run(b))
