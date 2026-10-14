# P05r — Pevný metadata inspektor a striktní IPC

Stav: inspektor je implementovaný jako oddělený interní entrypoint binárky agenta.
Native transient-unit launcher a veřejný opt-in probe CLI ještě nejsou zapojené.
System resource activation gate, VM akceptace i P05/checkpoint B zůstávají otevřené.
Preview.4 se nemění; žádný deploy ani transient systemd unit se v této etapě nekoná.

## Oddělení od běžného agenta

`--internal-sandbox-inspector` se vyhodnotí před běžným flag/config zpracováním.
Nepovoluje další argumenty (včetně --config), nevolá agent.Load/Run, telemetry ani
controller připojení a neotevírá journal. Není to resource action ani veřejný
setup příkaz, nepodporuje arbitrary command/unit paths nebo shell strings.

Budoucí trusted launcher mu předá jediné bounded JSON přes stdin a potvrdí
procesový exit a úklid jednotky samostatně. Sám inspektor žádnou jednotku
nevytváří, nepovoluje grants, neimpersonuje účet a nespouští runtime/script akce.

## Vstup a výstup

Private version-1 request obsahuje service UID/GID, opaque source/request SHA256
bindings a seřazené unikátní executable/working_directory páry. Žádné argv,
environment, resource/action IDs, config/journal paths nebo PEM. Encoder váže
request na validní P05q source/config snapshot; private wire bytes nejsou preview
ani běžný log. Maximum stdin je 1 MiB a 128 párů, path nejvýše 4096 bytes.

Parser odmítá unknown fields, duplicitní klíče včetně nested objects, nekanonický
case klíčů, nevalidní raw UTF-8, přebytečný dokument a excessive nesting. UID/GID
a SHA256 bindings musí být nenulové; digests mají canonical lowercase hex.
Paths jsou clean absolute Linux paths; hidden /home, /root, /run/user a host
/tmp, /var/tmp nejsou přijímané v této první script-only cestě.

Success response obsahuje pouze version/status, UID/GID, počet kontrol a digest
bindings/dependency proof. Selhání má pouze pevný kód; OS errors, paths a script
obsah se nezveřejňují. Dependency proof zahrnuje request i file fingerprints.
Response decoder je striktní a omezený na 4 KiB, ověřuje všechny expected bindings
i check count. **InspectorExited a UnitCollected zůstávají false**: JSON není
procesový exit, sandbox attestation ani důkaz úklidu. Launcher je musí skutečně
ověřit před úspěšným P05q Apply.

## Skutečný Linux proces

Inspektor běží již pod non-root `hlcp:hlcp`; sám nemění UID/GID ani privileges.
Kontroluje skutečné real/effective IDs, registered local service account/primary
group, /nonexistent+nologin contract a absenci supplementary groups.
Bounded `/proc/self/status` kontrola vyžaduje shodné čtyři UID/GID hodnoty,
NoNewPrivs=1, nulové permitted/effective/inheritable/ambient capabilities a jen
primary group nebo prázdný groups list. Na jiné platformě se identita odmítá.

Native ScriptFiles sdílí P05n no-follow/bounded read/ownership/permissions/single-link
kontroly executable, interpreter a pracovního adresáře. Nikdy soubor neprovádí.
Identita se ověřuje před i po kontrolách. Metadata nejsou execution/loader/ACL/MAC
nebo mount proof. ProtectHome/PrivateTmp/ProtectSystem a namespace musí potvrdit
budoucí launcher podle skutečné fixed jednotky; tento inspektor je sám neattestuje.

Inspections mají 10s cooperative context. Bounded input reader se ale může blokovat
na otevřeném stdin a filesystem/NSS call nemusí honorovat context: launcher musí
close/bound stdin, enforce whole-process timeout/kill/reap a limitovat stdout/stderr.
Žádné tvrzení, že samotný Go context přeruší arbitrary reader nebo kernel I/O.

## Evidence a navazující práce

Portable testy: striktní IPC, input/output limity, duplicate/case/unknown fields,
binding/identity/count mismatches, zero proofs, redakce, cancellation, output failure,
oddělený agent entrypoint a fuzz seeds. Linux testy kontrolují /proc status parser
a explicitní fixture v disposable containeru: skutečný system account, NoNewPrivileges
nastavené před exec přes test-only setpriv, read-only script marker ve skutečně
writable workdir, odmítnutí chybějícího NNP a unsafe script permissions.
To není booted systemd/ProtectHome/PrivateTmp/ProtectSystem test.

Windows full test/vet, Linux full race/test/vet, native disposable fixture a ARM64
compile-only jsou lokální evidence. Navazuje bounded native launcher/transport,
process/cleanup evidence, explicitní probe CLI a source-bound čerstvost evidence
před activation. Žádná evidence se zatím nepersistuje ani nepřijímá pro start.
