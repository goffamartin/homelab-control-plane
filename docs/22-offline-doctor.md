# Offline doctor — první blok P04c

Implementováno 2026-10-14, zatím pouze ve zdrojové větvi, ne v preview.4.

```console
hlcp doctor --server-name 192.168.0.15
hlcp-controller doctor --server-name 192.168.0.15 --json
hlcp-agent doctor --config /home/martin/.config/hlcp/agent.yaml
hlcp doctor --role agent --config /home/martin/.config/hlcp/agent.yaml
hlcp doctor --help
hlcp-agent doctor --config /home/martin/.config/hlcp/agent.yaml --host-checks --scope user
hlcp-agent doctor --config /home/martin/.config/hlcp/agent.yaml --host-checks --scope user --unit hlcp-agent-podman.service
```

Controller role standardně čte controller.crt/controller.key/ca.crt v
/etc/hlcp/controller; explicitní absolutní cesty lze zadat přes --cert/--key/--ca.
Agent role čte TLS cesty a identitu ze své konfigurace; controller TLS přepínače
se pro agent role nepoužijí. User agent nemá implicitní HOME/SUDO_USER discovery:
cestu konfigurace zadejte explicitně. Privátní root soubory vyžadují odpovídající
přístup (typicky sudo pro controller); doctor jejich práva neopravuje.

## Co kontroluje

- Linux amd64/arm64 platformu; jiný OS dostane upozornění a native filesystem
  kontroly odmítnou předstírat POSIX ownership.
- Bounded agent YAML (1 MiB), stejné strict schema a semantic validátory jako Load.
- Syntaxi controller endpointu včetně číselného portu.
- Pouze nakonfigurované runtime/executable soubory: PATH lookup a základní kontrolu
  regular/executable/non-writable file a owner. Nic nespouští; nejde o test
  interpreteru, script argv/cwd, rodičů executable, socket access ani Podman health.
  Script-only agent nevyžaduje Docker/Podman.
- Bounded cert/key/trust čtení, absolutní clean cesty, non-symlink ancestors,
  regular single-link files, owner root nebo aktuální effective UID,
  group/other write zákaz a private input bez group/other read.
- Key match, leaf/CA rozlišení, leaf validity, role EKU, agent node URI;
  controller hostname coverage jen s --server-name.
- Strict CA PEM bundle a validity každého CA. Expiry <=30 dní je warning,
  přesný okamžik NotAfter již failure; NotBefore v budoucnu také failure.

Nemění obsah/práva souborů, nevytváří state/lock/backup, neotvírá journal/ledger,
nevolá setup Apply/Recover, žádné aktivační service/script příkazy ani remote HLCP spojení.
Čtení může stejně jako jiné čtení ovlivnit filesystem atime; nejde o snapshot
garanci proti útočníkovi se stejným UID/root ani proti změnám celé directory tree.
Linux read používá NOFOLLOW/NONBLOCK a porovnání pre-open/FD identity:
FIFO/device/symlink substituce se nepřečte jako běžná konfigurace.

## Výstup a návratový kód

Text nebo --json: read_only=true, offline=true, checks s code/state/message.
Stavy ok/warn/fail/skip. Výstup neobsahuje raw parser/OS chyby, config contents,
PEM, tokeny, argv/env nebo nakonfigurované script cesty. Nepředávejte secrets
v přepínačích; ty neslouží pro zadávání klíčového materiálu.

Exit 0: žádný fail (warnings/skips **neznamenají kompletní připravenost**).
Exit 1: diagnostický fail. Exit 2: nesprávné options nebo chyba výstupu.
Help funguje bez konfigurace, API tokenu a běžící služby.

## Otevřené brány P04c/P05

Tento blok není kompletní doctor ani hotové P04:
explicitní jiný runtime účet při root inspekci, script interpreter/cwd readiness,
transaction inspect a opt-in bounded network/TLS peer probe zatím chybí.
Remote peer chain verification není nahrazena kontrolou lokálního CA bundle;
issuer leafu může být jiný než trust pro peer a nebude se automaticky předpokládat
společná CA. Controller env/API policy/node allowlist zatím nevaliduje.
Setup wizard a aktivace služby následují v P05.

Testy používají fake read-only Host bez mutačních/síťových metod a skutečné Linux
soubory pro permissions/link/FIFO checks. Cert fixtures pokrývají expiry hranice,
NotBefore, hostname/node mismatch, key mismatch a malformed trust. Report nečte
journal ani generic-agent runtime; oversized/invalid inputs a usage chyby jsou
secret-free. Publikované preview.4 tento příkaz neobsahuje.

Lokální evidence 2026-10-14: Windows full test/vet prošly; po doplnění port/runtime
testu prošly i cílené doctor testy. Finální Linux full race/test/vet prošly v
golang:1.27.2-bookworm bez sítě. Linux arm64 doctor test binary compile-only
prošel (nikoli živá ARM akceptace). Všechny tři skutečné CLI doctor --help
vstupy prošly bez načtení tokenu/konfigurace; git diff --check čistý.

## Navazující host readiness blok

Standardní invocation zůstává čistě souborová. --host-checks výslovně povolí
kernel clock query a pevně vymezené lokální systemd property dotazy.
--scope system je default; controller podporuje pouze system scope.
--scope user spouštějte přímo jako runtime uživatel, nikdy přes sudo.
Effective UID a primární GID se ověří přes OS account lookup; HOME/SUDO_USER
nejsou zdrojem identity. NSS lookup respektuje OS konfiguraci (např. vzdálený
directory provider); offline zde znamená bez remote HLCP probe, ne potlačení NSS.
Jiný účet pomocí root impersonation tento blok nepodporuje.

Clock query používá Linux adjtimex s Modes=0, bez změny času a bez aktivace
timedated. Kontroluje kernel STA_UNSYNC; nejde o potvrzení dostupnosti NTP serveru.

Service queries používají jen root-owned /usr/bin/systemctl a /usr/bin/loginctl
po kontrole vyřešené cesty/ancestorů, bez PATH fallback nebo shellu:
show HLCP unit, show user@UID.service a show-user UID Linger.
Žádné start/enable/reload/enable-linger operace. Čtecí D-Bus dotaz není
garance nulového manager cache/log účinku, ale nezadává aktivaci cílové služby.
Pro každý probe platí 3s deadline, 250ms wait-delay a 4KiB stdout/stderr limit.
Environment je allow-list, bez inherited secrets a externích D-Bus adres.
User bus adresa se sestaví z ověřeného effective UID a private /run/user/UID.
Raw stderr a property hodnoty/cesty se do reportu nevypisují.

Kontroluje loaded/masked/missing, active/failed, persistent/runtime-only enablement
a existenci lokálního fragmentu/drop-in. Zjištěná override je upozornění, nikoli
chyba nebo pokyn k odstranění; vendor content se neporovnává s balíčkem.
Agent default unit je hlcp-agent.service; starší rootless instalaci vyberte
výslovně přes --unit hlcp-agent-podman.service. Arbitrary unit/command nepodporuje.
User scope doplní user manager a linger. Pouze pokud validní agent konfigurace
obsahuje Podman, přečte /etc/subuid a /etc/subgid a ověří odpovídající username/UID,
numeric bounds a souvislý rozsah alespoň 65536 IDs. Nepotvrzuje absence overlaps
s jinými účty, NSS subordinate provider, mapping helpers, storage/cgroups/socket
ani úspěšný start kontejneru. Generic/Docker profil tyto rozsahy nevyžaduje.

Unavailable systemd/logind je warning, nikoli předstíraný OK; masked/failed service,
root user-scope a chybějící požadované subid range jsou fail.
Nevalidní agent config ukončí agent diagnostiku ještě před host probes.
Kontrakty dotazů odpovídají systemd dokumentaci:
[unit properties](https://www.freedesktop.org/wiki/Software/systemd/dbus/),
[loginctl show-user/linger](https://www.freedesktop.org/software/systemd/man/252/loginctl.html).

Host-blok evidence 2026-10-14: Windows full test/vet a Linux full race/test/vet
prošly; Linux testuje i 4KiB overflow a timeout skutečného child procesu. Account,
unit/linger/manager/mask/override a subid hranice jsou testované přes fake query
provider; žádné živé VM/systemd acceptance tím není nahrazeno. Nápověda funguje
bez probes. Linux arm64 compile-only prošel, nikoli live ARM. Žádný nový preview.
