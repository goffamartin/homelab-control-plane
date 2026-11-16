# E4: silent agent interruption

Five successive pauses and resumes of one disposable agent process on the Debian
agent VM. The controller and agent used loopback, ephemeral certificates and
independent temporary state. SIGSTOP paused the agent without closing its socket.
The controller used the defaults of five-second heartbeats and an offline threshold
of three missed intervals. The observer polled at approximately 50 ms.

Each trial ends after a new online session and the first successful live status
result. The next trial begins immediately. The recovery times therefore include
backoff carried across brief sessions and are observations of successive interruptions.
The method models an unresponsive agent process. It does not inject packet loss or
estimate recovery across a WAN. UTC timestamps describe the start of each interruption.
