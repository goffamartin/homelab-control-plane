# P05l — Native agent activate pro počáteční imported setup

Zdrojová implementace od 2026-10-14. Příkaz není v publikovaném preview.4 a není
ověřeným live systemd/VM deployem. P05/checkpoint B zůstává otevřený.

## Použití a aktuální omezení

```sh
# Přímo jako skutečný owner, bez sudo: default user manager / generic unit.
/usr/bin/hlcp-agent activate
/usr/bin/hlcp-agent activate --enable --apply --yes

# Compatibility name, stále stejný user manager a stejné config/state paths.
/usr/bin/hlcp-agent activate --unit hlcp-agent-podman.service

# Oddělený system manager; root caller, ale runtime účet hlcp.
sudo /usr/bin/hlcp-agent activate --scope system
sudo /usr/bin/hlcp-agent activate --scope system --enable --apply --yes
```

Výchozí režim jen ukáže plán. --apply a --yes vyžadují jeden druhý; --enable
samostatně požaduje persistent boot aktivaci. --json dává redigovaný report.
Help/neplatné options neprovádějí host inspection. Žádný restart aktivní služby,
stop/reset peeru, automatický linger nebo account/config změna.

Původní P05l bridge vyžadoval prázdné resources/capabilities. Navazující P05n
rozšiřuje user scope o read-only metadata preflight Docker/Podman/scripts a
status-only systemd. System-scope resources a mutující user-scope systemd zůstávají
RESOURCE_PREFLIGHT_REQUIRED: root práva nejsou práva sandboxu služby a neověřujeme
polkit grants. Samotné capabilities bez resources nezavádějí runtime závislosti.
[Přesný kontrakt a omezení P05n](36-native-resource-readiness.md).
Metadata nejsou zárukou skutečného prostředí či úspěchu akce. Stávající služby
mohou dál běžet dosavadním způsobem; tento příkaz je nepřestavuje.

Podporuje jen odpovídající Debian package binary /usr/bin/hlcp-agent a pevné
distribuované profily. Archivová/manual binary, custom unit/drop-ins a jiné config
paths se odmítají. Windows/ostatní platformy nejsou native aktivací podporované.

## Ověření profilu a účtu

System caller musí být skutečné root UID/GID 0. Runtime hlcp účet musí již
existovat s nerootovým UID/GID, stejnojmenným primary group, bez dalších groups,
home /nonexistent a lokálním nologin/false passwd záznamem; NSS IDs se ověřují.
Stejná sdílená read-only account validace zůstává pro controller profile.
Žádné vytvoření/opravování účtu nebo Docker group grant.

User scope vyžaduje přímo registrovaný nerootový owner/primary GID. Home se bere
z registrace, ne z HOME/SUDO_USER/XDG. /run/user/UID musí být jeho nesymlinkový
private mode-0700 adresář pod root-owned bezpečnými /run a /run/user; bus musí
být socket stejného UID/GID. Ověří se active user@UID.service a explicitní
logind Linger=yes/no. Persistent user enable vyžaduje již připravený linger.

Vybraná unit musí být loaded, active/inactive, enabled/disabled, s
NeedDaemonReload=no, bez drop-ins, z /usr/lib/systemd/system nebo user přesně
podle scope. System User/Group jsou hlcp; user profil nemá User/Group override.
Obsah musí odpovídat embedded distribuované unit včetně package binary cest.

User peer unit musí být buď skutečně absent, nebo stejně validní distribuovaná
unit v inactive/disabled stavu. Jiný source, mask/override, aktivní/failed/
transitioning/enabled peer se nepřevezme ani automaticky nezastaví. Conflicts
nenahrazuje kontrolu dvou journal writers. Jiné managery nejsou koordinované.

## Config, TLS, journal

Pět úplných privátních mode-0600 imported souborů se čte přes pinned planner;
pending/invalid setup transakce odmítá aktivaci, committed retained backup se
nemění. Bounded YAML Parse/Validate, endpoint/server-name syntax, přesná node URI,
client-auth/key-match/chain/expiry a oddělený vlastní issuer/controller peer trust.
Při každém pre/post-check se TLS validuje proti novému aktuálnímu času.

User config je v registrovaném ~/.config/hlcp, TLS files v této directory,
journal přesně ~/.local/state/hlcp/command-journal.json. System provisioning je
/etc/hlcp/agent, runtime TLS paths přesně /run/credentials/hlcp-agent.service a
journal /var/lib/hlcp/command-journal.json. Žádné arbitrary external file reads.

Journal se neotevírá, jeho obsah/schema/node identity se předem neověřuje a lock
se nezískává. Kontrolují se jen bezpečné ancestor directories a případná file
metadata: private runtime-owner directory/file, regular single-link journal,
bez symlinků a permission repair. Absent state může vzniknout při schváleném
startu runtime/systemd; dynamic state inode existence proto není v immutable
startup proof. State musí po startu splnit stejná ownership/mode pravidla.
Root-owned legacy hlcp state se implicitně nechownuje na nový service account.

Proof spojuje account/current groups, installed binary/systemctl, units,
runtime dir/bus a config/TLS snapshots; P05k přidává caller/peer/linger evidence.
Neobsahuje dynamic active/enabled vybrané služby. Nikde se nevypisuje.

## Operace a failure hranice

Pouze fixed systemctl show/enable/start; loginctl jen show-user aktuální UID.
User příkazy mají --user a explicitně odvozený ověřený runtime/bus environment;
system manager dotaz na user@UID.service zůstává jen read-only readiness.
Sanitizovaný PATH/locale/env, fixed trusted root-owned executable, žádný shell,
remote bus, sudo nebo user-provided executable. --no-reload potlačuje i implicitní
reload při enable. Show 3s, enable/start 30s, stdout/stderr každý 16 KiB a bounded
pipe wait; environment neobsahuje původní HOME, credentials ani bus overrides.

Consent/recheck/order/partial results jsou P05k/P05f shared lifecycle. Po startu
může runtime otevřít journal, navázat mTLS a registrovat se; active systemd stav
není důkaz online registration. Při timeoutu nebo neověřeném post-check může
manager job dál běžet. Žádný rollback, retry, disable/stop, reset state či journal.
Preflight a manager operation nejsou atomické proti privilegované/souběžné správě.
Metadata/mode checks nejsou plný audit ACL/MAC, mounts nebo loader dependencies.

Exit 0 preview/applied/no-op; 1 PREFLIGHT_REFUSED, PEER_SERVICE_CONFLICT,
LINGER_REQUIRED, RESOURCE_PREFLIGHT_REQUIRED, STALE_PLAN či ACTIVATION_INCOMPLETE;
2 syntax/output failure. Neověřený výsledek má uncertain=true a read_only=false;
zachovat partial completed, nejdřív status/journal a read-only doctor pod správným
účtem. Chyba stdout po provedené změně není rollback.

## Evidence / další práce

Windows full test/vet a Linux full race/test/vet prošly. Native helper testy
ověřují strict properties/unknown/duplicate/oversize, absent peer vs selected,
fixed-command refusal, skutečný imported config/TLS/name/expiry, resources gate,
journal metadata bez změny unknown history a symlink/public state refusal.
CLI tests ověřují offline help/options bez inspection, preview bez mutation,
consent, linger/peer refusal a redigované partial results. Actual offline help
a isolated container fixture se skutečnými hlcp/hlcp-controller účty prošly;
sdílená service-account validace nevyžadovala žádnou změnu host účtů.
ARM64 compile-only prošel. Žádné tvrzení o úspěšném booted-systemd startu,
user bus/logind/reboot, live Podman/Docker nebo live ARM.

Navazuje runtime-specific resource readiness, service/account/state provisioning
a skutečný fresh Debian package/setup + VM/reboot acceptance checkpointu B.
Publikované preview.4 a konfigurace aktuálního homelabu se tímto blokem nemění.
