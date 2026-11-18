# E4: clean stop and relaunch

Five consecutive trials on the Debian agent VM. The installed controller
executable was copied from the controller VM and ran beside a disposable agent
using separate loopback ports, credentials, journals and ledger. A private CA
authenticated the test session. The agent could query status on the dedicated
benchmark container.

The driver closes the wrapper's input, which signals the agent to terminate,
then queries until it sees offline. It relaunches the agent and polls for online
with a different session ID at intervals of approximately 50 ms. It then submits
a live status command and requires a successful running result. Timings include
local process handling and the observer's polling delay. They describe graceful
shutdown and relaunch over loopback. Silent loss, heartbeat detection, automatic
reconnect during a continuing process, and WAN latency were outside this run.

The first setup attempt on the controller VM refused startup because its
managed controller state already existed. It yielded no timing samples.
