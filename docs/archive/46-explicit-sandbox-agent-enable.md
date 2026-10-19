> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05x — Explicit boot enable after verified sandbox start

Stav: implementováno ve zdrojích, nepublikováno v preview.4. VM beze změn.
P05/checkpoint B, booted Debian sandbox/collection/registration/reboot acceptance
zůstávají otevřené. Lokální testy nenahrazují skutečný start ani reboot.

## Rozhraní a pořadí

```text
sudo hlcp-agent probe-resources --start --enable --json
sudo hlcp-agent probe-resources --start --enable --json --apply --yes
```

První příkaz je read-only plán. Druhý provede P05w initial probe → matching fresh
probe → ověřený start → explicit enable. Vyžaduje instalovanou binárku obsahující
tento blok; preview.4 jej neobsahuje. `--enable` vyžaduje `--start` a potvrzené
mutace dál vyžadují oba `--apply --yes`. Bez enable se reboot nastavení nemění.
Není standalone enable-only, restart, stop, disable či reset-failed příkaz.

Enable následuje až po ověřeném active postchecku. Pokud start selže, workflow
nově nezapne boot aktivaci. Byla-li služba již enabled, ověří se skutečný stav
bez zbytečného enable commandu; případný již existující boot enable se neruší.
Fixed scope/unit/identity/profile/config a script-only omezení zůstávají.

Core `StartPersistent` sdílí single-use handoff se Start/Apply a kopiemi plánů.
Trusted package-private enable adapter se vyžaduje před další probe mutací.
Native adapter kontroluje source, active stav a monotonic expiry těsně před
fixed `systemctl enable hlcp-agent.service`. Po commandu ověří jen očekávanou
disabled → enabled změnu, stále active agenta, absent probe a nezměněný source.
Runtime-only/masked/failed/transitional profily zůstávají odmítnuté.

Source fingerprint nyní normalizuje Active i Enabled lifecycle vlastnosti,
ale skutečné `AgentInactive`/`AgentEnabled` jsou v snapshotu odděleně. Plány a
probe porovnávají celý snapshot; normalizace není povolení ignorovat nečekané
enable změny. Start postcheck vyžaduje nezměněný enabled stav. Enable postcheck
smí změnit jen AgentEnabled na true, žádnou jinou vazbu.

Start/enable fáze má společný 45s context, start command nejvýše 20s, enable 10s,
read kontroly kratší. Celý potvrzený CLI workflow je dál omezený na 90s a stáří
důkazu na původní 2 minuty; dlouhá práce tyto limity neprodlužuje.

## Částečný výsledek

Report přidává `enable_requested` a `start.enabled` (ověřený stav, nikoli počet
nových změn). Po enable chybě zůstává `started=true`, pokud byl start ověřený,
a `uncertain=true`. `enabled=false` nezaručuje, že command nic nezměnil — znamená,
že enabled stav není ověřený. Žádný automatický stop/disable/rollback/retry.
Start může běžet, i když enable selže; uživatel musí prohlédnout skutečný stav.

Enable nepřidává runtime práva ani resource akce. Future reboot je běžný systemd
start již schválené konfigurace, ne opakování onboarding probe či trvalý readiness
grant. Změny závislostí, runtime allowlist, expiry a kontrola výsledků nadále platí.
Verified active/enabled není controller registration ani záruka zdraví po rebootu.

## Lokální evidence a další krok

- CLI help/options/preview bez mutací, enable bez startu nebo neúplný consent
  odmítnuté před config I/O, unsupported adapter před probe dispatch.
- Fresh/matching probes → start → enable; already-enabled bez dalšího enable,
  bez flagu žádný enable, start/probe chyba zabrání enable.
- Enable chyba zachová ověřený start a uncertain; single-use po kopii plánu.
- Source comparators povolí pouze očekávané lifecycle změny; native test
  executable/zero proof nikdy nedispatchují enable.
- Windows full test/vet, Linux full race/test/vet a ARM64 compile-only.

Navazuje booted Debian VM acceptance s transient cleanup, agent registration,
restartem systému a ověřením bez loginu. Nový release až po zbývajících branách.
