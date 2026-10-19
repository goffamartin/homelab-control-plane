> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05v — Source-bound one-shot system agent start adapter

Stav: implementovaný composite lifecycle core a Linux native start adapter ve
zdrojích. Původně bez veřejného CLI; confirmed `probe-resources --start` navazuje
v [P05w](45-confirmed-sandbox-start-cli.md). Původní `activate` se nemění. Nejde o
nový release ani ověřený běh v booted systemd; preview.4 a VM jsou beze změn.
P05/checkpoint B a veřejná system resource activation gate zůstávají otevřené.

## Pevně spojené ověření a start

`SandboxReadiness.Start(ctx, confirmed)` není přijetí již uloženého/aplikovaného
proof. Musí mít původní úspěšný probe ve stejném procesu a trusted host s
package-private start adapterem. Externí callback, JSON proof, arbitrary unit,
scope, command nebo impersonation se nepřijímají.

1. Souhlas a existence trusted start adapteru před jakoukoli další mutací.
2. P05u revalidation: stejné source/config/identity a matching dependency digest,
   ověřený exit i collection nové transient jednotky. Handoff je jednorázový.
3. Poslední root/source kontrola a kontrola původního 2min stáří **po** source
   čtení. Zdroj musí pořád potvrzovat inactive agenta a neexistující probe.
4. Native adapter znovu ověří skutečný source i monotonic expiry po čtení a spustí pouze pevný
   `systemctl start hlcp-agent.service` přes existující sanitizovaný bounded transport.
5. Native postcheck ověří actual active stav a nezměněné config/TLS/binaries,
   unit/profile/account/identity, enabled stav a neexistující probe.

Pouze expected inactive → active se normalizuje v source fingerprintu pro
postcheck; actual `AgentInactive` zůstává zvlášť ověřený. Probe/planning volání
stále vyžadují inactive a nepřijmou active/failed/transitional agenta.

Start fáze má 30s context, systemctl nejvýše 20s a sdílené kratší read limity.
Kopie i nezávislé readiness instance sdílejí jediný původní handoff. Samostatně
provedené `Apply` jej již spotřebovalo a následné `Start` je stale — žádné
neomezené oprávnění ze starého privátního proof.

## Rozsah a nejasné výsledky

Tento blok pouze startuje neaktivní system generic agent se script-only resources
pod již ověřeným profilem. Nevytváří účty/oprávnění/config, neprovádí resource
akce, enable, restart, stop, reset-failed ani rollback. Start přirozeně zahájí
normální agent runtime, včetně journal lifecycle a outbound mTLS spojení;
na rozdíl od samotného probe tedy nejde jen o metadata operaci.

Výsledek uvádí dokončení probe, ověřený start a celkové uncertain. Pokud start
transport nebo postcheck selže, `started=false` znamená **neověřený start**, ne
záruku, že služba nebyla spuštěna. I selhání po probe, ale před startem není
reportované jako úplně read-only. Po dispatch žádný automatický retry/rollback.
Člověk musí prohlédnout skutečný stav před dalším workflow.

Metadata revalidation není filesystem lock. Souběžná změna souborů po poslední
kontrole zůstává TOCTOU hranicí; proof není zárukou vykonatelnosti ani výsledku
skriptu. Active postcheck není důkaz připojení k controlleru/inventory/health.
Explicit enable a jeho source/lifecycle změna zůstávají samostatnou navazující
etapou, nikoli vedlejším účinkem startu.

## Lokální evidence a další krok

- Core fake-host testy: consent, revalidation před startem, single-use/kopie,
  souběžné plány, nemožnost přijetí JSON nebo již applied proof.
- Source/caller/dependency změny, cancellation, start error a expiry během
  posledního source čtení: žádný neověřený úspěch či automatické opakování.
- Post-start source comparator přijme jen inactive → active; odmítne změny
  source/config/UID/GID/probe a stále inactive stav.
- Linux native test binárka a zero proof jsou odmítnuté před service dispatch.
- Windows full test/vet, Linux full race/test/vet a Linux ARM64 compile-only.

Následuje confirmed CLI orchestrace initial probe → revalidation → start,
explicit enable kontrakt a booted Debian VM sandbox/collection/start/reboot
acceptance. Lokální fake/refusal testy skutečnou VM akceptaci nenahrazují.
