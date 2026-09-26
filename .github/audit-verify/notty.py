# Start env-vault exec in a new session (no controlling terminal), signal only
# env-vault, and report what the child saw and how env-vault ended.
import os, signal, subprocess, sys, tempfile, time

def run(binary, signame):
    d = tempfile.mkdtemp()
    log = os.path.join(d, "log")
    child = ('trap "echo int >> $0; exit 0" INT; trap "echo quit >> $0; exit 0" QUIT; '
             ': > $0.ready; while :; do sleep 0.05; done')
    proc = subprocess.Popen([binary, "exec", "--", "sh", "-c", child, log],
                            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                            stderr=subprocess.DEVNULL, start_new_session=True)
    for _ in range(100):
        if os.path.exists(log + ".ready"):
            break
        time.sleep(0.05)
    proc.send_signal(getattr(signal, signame))
    try:
        rc = proc.wait(timeout=3)
        state = f"env-vault exit={rc}"
    except subprocess.TimeoutExpired:
        state = "env-vault STILL RUNNING"
        os.killpg(proc.pid, signal.SIGKILL)
        proc.wait()
    got = open(log).read().split() if os.path.exists(log) else []
    return f"{os.path.basename(binary)} {signame}: child got={got} {state}"

for b in sys.argv[1:]:
    for s in ("SIGINT", "SIGQUIT", "SIGTERM"):
        print(run(b, s))
