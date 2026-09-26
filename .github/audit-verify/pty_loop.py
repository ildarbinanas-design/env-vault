import os, pty, sys, time, select, tempfile, subprocess
def run(binary):
    d = tempfile.mkdtemp()
    cnt = os.path.join(d, "count")
    child_script = f'''trap 'echo i >> {cnt}' INT; sleep 3'''
    loop = f'''for i in 1 2 3; do {binary} exec -- sh -c "{child_script}"; echo "after $i rc=$?"; done; echo loop-done'''
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp("bash", ["bash", "--norc", "--noprofile", "-c", loop])
    time.sleep(1.0)
    os.write(fd, b"\x03")  # Ctrl+C via the terminal line discipline
    out = b""
    end = time.time() + 15
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.5)
        if r:
            try:
                chunk = os.read(fd, 1024)
            except OSError:
                break
            if not chunk: break
            out += chunk
        try:
            wpid, st = os.waitpid(pid, os.WNOHANG)
            if wpid: break
        except ChildProcessError:
            break
    n = len(open(cnt).read().split()) if os.path.exists(cnt) else 0
    return out.decode(errors="replace").replace("\r", "").strip().splitlines(), n
for name in sys.argv[1:]:
    lines, n = run(name)
    print(os.path.basename(name), "| SIGINT received by child:", n, "| shell output:", lines[:4])
