# P05g — Native Debian controller activation CLI

Implementováno ve zdrojích od 2026-10-14. Publikované preview.4 tento příkaz nemá;
žádný nový release ani změna běžících VM. P05/checkpoint B zůstává otevřený.

## Oddělený příkaz

Po instalaci odpovídajícího Debian balíčku a dokončení imported setup:

```sh
# Read-only náhled: chce jen start, nikoli trvalé zapnutí po bootu.
sudo /usr/bin/hlcp-controller activate --server-name 192.168.0.15

# Náhled se samostatně požadovaným trvalým enable.
sudo /usr/bin/hlcp-controller activate --server-name 192.168.0.15 --enable

# Explicitně potvrzená aktivace.
sudo /usr/bin/hlcp-controller activate --server-name 192.168.0.15 --enable --apply --yes
```

`--server-name` je ověřované DNS/IP serverového certifikátu, nikoli nová konfigurace.
`--json` poskytne report bez evidence a tajemství. `activate --help` je offline,
nevyžaduje root ani soubory. Neplatné options/help nedosáhnou host adaptéru.
Žádná implicitní aktivace z `setup`, žádný interaktivní ani remote run-as režim.

Příkaz požaduje skutečné UID/GID 0 a běh z instalované `/usr/bin/hlcp-controller`.
Windows/ostatní OS fail closed; `go run`, test executable nebo ruční archivový
profil neaktivují službu. Podporuje jen fixed `/etc/hlcp/controller` a distribuovanou
`/usr/lib/systemd/system/hlcp-controller.service`, nikoli custom directories/units.

## Preflight

- Služba musí být loaded, active/inactive, persistently enabled/disabled, bez
  drop-ins a s `NeedDaemonReload=no`. Failed/transitional/masked/runtime-only,
  overrides, jiný service User/Group nebo fragment path se odmítají.
- Obsah unit musí přesně odpovídat profilu vloženému z distribuované šablony,
  s package binary cestou `/usr/bin/hlcp-controller`; pouze CRLF normalizace.
  Žádný automatický daemon-reload ani přepis custom unit.
- Účet `hlcp-controller` musí existovat jako nerootový, s vlastním stejnojmenným
  primary group, bez dalších skupin, home `/nonexistent` a lokálním passwd záznamem
  s nologin/false shell. NSS forward/reverse IDs se ověřují. Účet se neopravuje,
  shadow se nečte a login/password policy se nemění.
- Instalované controller/systemctl musí být root-owned, single-link ELF regular
  executables, bez setuid/setgid a group/world write, pod bezpečnými root-owned
  adresáři. Čtení má limit 64 MiB; evidence uchovává digest a inode/mode/owner.
- Devět úplných root-owned mode-0600 config souborů z imported setup se čte přes
  pinned setup planner. TLS, vlastní issuer i peer trust, key match, SAN/EKU/
  chain/platnost, bind, tokens/policy/admission se ověřují sdílenými validátory.
  Policy credential paths smějí odkazovat jen na api.token/observer.token.
  Validní manuální env komentáře/policy formát se zachovají, nic se nepřepisuje.
- Neuzavřená/poškozená setup transakce znamená refusal. Platná retained committed
  backup není automaticky zahozena a nebrání aktivaci.

Inspekce neotvírá ledger ani listener a negeneruje tokeny. Proof spojuje account,
unit/binaries a konfigurační snapshoty, ale nikde se nevypisuje. Každý pre/post-check
znovu ověřuje TLS proti aktuálnímu času, tedy i expiraci během aktivace.

## Změny a výsledky

[P05f core](28-controller-activation-core.md) porovná snapshot před mutací.
`enable` je jen na přání a jen pokud chybí. `start` je jen pro inactive; běžící
služba je no-op, nikdy restart. Active služba tedy nepřevezme nové diskové
credentials/config — runtime snapshoty zůstávají restart-only a explicitní
restart/migrace nejsou součástí tohoto příkazu.

Systemctl má pevné argumenty jen pro show/enable/start této jediné system unit;
žádný shell, PATH-selected program, remote bus, implicitní sudo, reset-failed,
enable --now nebo stop. Sanitizované prostředí, stdout/stderr každý 16 KiB.
`--no-reload` potlačí i implicitní manager reload při enable; boot symlinky
se vytvoří, start je výslovný samostatný krok.
Timeout show 3 s / mutace 30 s a omezený wait na zavření process pipes.
Po každé změně se ověří očekávaný stav; neověřený výsledek je uncertain.
Timeout/kill klienta neznamená zrušení již přijatého systemd jobu.

Start již skutečně aktivuje runtime, který může otevřít ledger/listenery a přijmout
povolené agenty. Při selhání se neprovádí rollback souborů, disable, stop ani retry.
Zachovat partial result a nejdřív zkontrolovat skutečný stav služby/journal.
Preflight a systemd operace nejsou atomické proti souběžné root správě; root-owned
path kontroly nenahrazují zámek proti privilegovanému správci ani MAC/ACL policy.

Exit 0: preview/applied (včetně no-op); 1: PREFLIGHT_REFUSED, STALE_PLAN nebo
ACTIVATION_INCOMPLETE; 2: syntax/output failure. Read-only false či uncertain
znamená, že změna mohla nastat. Chyba výstupu po akci není rollback.
Při refusal použít read-only doctor, status/journal a inspekci instalovaného
profilu; neobcházet kontroly force ani mazáním state.

## Evidence a další blok

Windows full test/vet a Linux full race/test/vet prošly. Native filesystem tests
ověřují kompletní setup včetně retained backup, missing/private tokens, name/
expiry refusal, preservaci, artifact permissions/limits/symlinks a bounded output.
CLI testy ověřují help/invalid options bez host access, preview bez mutace,
consent a redigovaný partial outcome. Strict property parser testuje overrides,
duplicate/unknown fields a nepodporované systemd stavy. Jednorázový offline Linux
kontejner ověřil skutečný účet vytvořený stejným postupem jako Debian postinst.
ARM64 je compile-only; žádné live ARM nebo booted-systemd start/reboot tvrzení.

Navazuje bezpečné user CLI provisioning, agent user/system service integrace
a booted Debian VM acceptance před checkpoint B/release. Managed PKI, automatická
obnova a snadnější stažení zůstávají dalšími etapami schváleného plánu.
