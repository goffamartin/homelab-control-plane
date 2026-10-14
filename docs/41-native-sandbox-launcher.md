# P05s — Bounded native sandbox launcher

Stav P05s: implementovaná launcher/transport komponenta. Native source host a
veřejný probe CLI navazují v [P05t](42-native-resource-probe-cli.md). Na VM ani v booted systemd nebyla
spuštěná. System resource activation gate a P05/checkpoint B zůstávají otevřené;
preview.4 se nemění. Tento blok nedokazuje live sandbox ani cleanup akceptaci.

## Pevné spuštění a ověřené pořadí

Launcher smí vytvořit jen `hlcp-agent-resource-probe.service` a spustit installed
`/usr/bin/hlcp-agent --internal-sandbox-inspector`. Žádný arbitrary unit/command,
shell, config flags, user/remote manager, start/enable hlavního agenta či grants.
Native transport vyžaduje skutečný root UID/GID, booted lokální systemd, validní
`hlcp` account a trusted root-owned ELF binaries. Ne-booted build container se odmítá.

1. Validace P05q snapshot/input binding a read-only potvrzení neexistující jednotky.
2. Start pevného inspektoru s drženým stdin: žádná request data se zatím neposílají.
3. Read-only ověření skutečných unit properties: transient/active/type/PID,
   přesný fragment path, prázdné drop-ins/supplementary groups, User/Group hlcp,
   NoNewPrivileges, PrivateTmp, ProtectHome, ProtectSystem, StateDirectory/mode,
   UMask, address families, syscall architecture a lifetime/cleanup pravidla.
4. Teprve potom bounded request přes pipe. Čekání na exit 0 a striktní P05r response.
5. Read-only potvrzení, že jednotka zmizela; až tehdy lifecycle flags true.

Použité wait/pipe/quiet/collect chování vychází z
[Debian systemd-run manuálu](https://manpages.debian.org/trixie/systemd/systemd-run.1.en.html).
Collect je požadavek, nikoli důkaz úklidu; úspěch samotného klienta nestačí.
Nejprve se ověřuje skutečný sandbox, nikoli jen argv požadující jeho nastavení.
Pevný command nemá environment placeholders; cesty v JSON stdin nejsou CLI args.
Nepotřebujeme novější --expand-environment option a nezvyšujeme package minimum 252.

## Limity a selhání

Probe service: Type=exec, Restart=no, TimeoutStartSec=3s, RuntimeMaxSec=15s,
TimeoutStopSec=2s, KillMode=control-group, SendSIGKILL=yes, CollectMode=inactive-or-failed.
Limity odpovídají [service lifecycle manuálu](https://manpages.debian.org/trixie/systemd/systemd.service.5.en.html).
Agentův sandbox se zrcadlí; probe navíc nesmí restartovat sebe. StateDirectory může
být vytvořená/chowned systemd při potvrzeném probe: není to read-only systémová
operace, byť inspektor sám nic nezapisuje. TLS credentials se do probe nenahrávají.

Transport má 25s aggregate context (uvnitř 30s P05q Apply), jednotlivé show 2s,
bounded ready/collection polling, stdin 1 MiB, stdout a stderr nejvýše 4 KiB.
Client environment je sanitizovaný. Pipe writer se při cancellation uzavře;
client se cancel/kill/wait/reap, WaitDelay je 250ms. Přetečení je refusal, ne
truncation vydávaná za validní evidence. Private stdout/stderr/errors se nelogují.

Zabití klienta není důkaz zastavení server-side unit. Tu omezují její vlastní
timeouts; host/kernel I/O se může zablokovat i po SIGKILL. Neověřený exit,
malformed response, source/identity mismatch, cancelled operation nebo chybějící
collection znamená uncertain bez automatického opakování. Close neprovádí
systemctl stop/reset ani nesahá na cizí jednotku. Konflikt před startem je refusal.

Pro metadata jsou nově výslovně odmítnuté /run/credentials a standardní command
journal/sidecar paths; core odmítá také config.JournalFile/sidecars. Žádný journal
content, lock nebo source config certificate/key/CA path nesmí být zadán jako probe
executable/workdir. Nejde o identifikaci všech potenciálních PEM souborů na disku.
Obecný runtime contract tím není změněn; jde jen o inspektorovu omezenou cestu.

## Lokální evidence a další blok

- Portable fake transport: správné pořadí, nulový request před verified sandboxem,
  property/malformed/start/exit/response/collection/cancellation refusals, bez retry,
  fixed argument list, strict property parser a reserved paths.
- Linux skutečné test-only subprocesses: pipe stdin/EOF, waited exit, nonzero exit,
  flood refusal, cancellation, close bez requestu, opakovaný close/exchange.
  Spouští pouze test executable, nikoli systemd service/resource akce.
- Explicitní Debian 13 disposable syntax fixture: unit se odvodí z launcher args
  a ověří systemd-analyze verify. Inert ELF placeholder není proveden, jednotka
  není spuštěná; syntaktická validace není systemd/D-Bus sandbox akceptace.
- Windows full test/vet, Linux full race/test/vet a Linux ARM64 compile-only.

Navazuje native read-only source snapshot host (config/TLS/profile/account/state,
inactive main agent, absent probe, source/config bindings), confirmed CLI a
booted Debian VM akceptace. Evidence se dosud nepersistuje/nepřijímá pro activation;
její čerstvost a revalidation před startem zůstávají samostatnou bránou.
