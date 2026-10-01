"""Drive herdrmon in a pty: move to a slot, type a label, Enter.
usage: pty_drive.py DOWNS LABEL   (HERDRMON_BIN: the binary, default ./herdrmon)"""
import os, pty, sys, time, select, re

downs, label = int(sys.argv[1]), sys.argv[2]
binary = os.environ.get("HERDRMON_BIN", "./herdrmon")
pid, fd = pty.fork()
if pid == 0:
    os.environ["TERM"] = "xterm-256color"
    os.environ["COLUMNS"], os.environ["LINES"] = "120", "36"
    os.execv(binary, ["herdrmon"])

import fcntl, termios, struct
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 36, 120, 0, 0))
buf = b""
def pump(sec, until=None):
    global buf
    end = time.time() + sec
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.05)
        if r:
            try:
                buf += os.read(fd, 65536)
            except OSError:
                return False
        if until and until in plain():
            return True
    return True

def plain():
    return re.sub(rb"\x1b\[[0-9;?]*[a-zA-Z]", b"", buf).decode("utf-8", "replace")

pump(20, until="Choose your device")  # first frame (the terminal queries time out in a pty)
pump(2)                               # let discovery + probes land
for _ in range(downs):
    os.write(fd, b"\x1b[B"); pump(0.15)
for ch in label:
    os.write(fd, ch.encode()); pump(0.03)
pump(0.4)
os.write(fd, b"\r")
alive = pump(8)
for msg in ("Sending out", "Go! ", "is paralyzed! It can't move!", "has no energy left to battle!", "LABEL first"):
    print(f"{msg!r}: {msg in plain()}")
_, status = os.waitpid(pid, os.WNOHANG)
print("exited" if status or not alive else "still running")
