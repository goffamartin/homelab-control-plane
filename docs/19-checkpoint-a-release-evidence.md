# Checkpoint A — preview.4 release evidence

Datum: 2026-10-13. Etapy P01–P03 mají lokální akceptaci a publikovaný privátní
[preview.4](https://github.com/goffamartin/homelab-control-plane/releases/tag/v0.1.0-preview.4).
Nejde o dokončený plug-and-play setup ani o tvrzení, že již proběhla živá VM migrace.

## Provenance a artefakty

- Tag: `v0.1.0-preview.4`, prerelease, nikoli draft.
- Přesný commit: `2dd68dd3a4c87d4dc1ec54c9175e137e6879ec3c`.
- Toolchain: Go 1.27.2, žádná změna runtime dependencies.
- Dva nezávislé čisté buildy: `dist/p03-preview4-first` a
  `dist/p03-preview4-second`, bez `-allow-dirty`.
- Všech 16 souborů je byte-identických: devět binárek, deployment archive,
  čtyři .deb (controller/agent, amd64/arm64), manifest a SHA256SUMS.
- Lokální SHA256SUMS i manifest ověřeny. GitHub SHA256 digest a velikost všech
  16 publikovaných assets odpovídají lokálním souborům; remote tag míří na přesný
  čistý commit výše. Repozitář zůstává PRIVATE.
- Tato následná evidence není uvnitř původních release artefaktů; ty obsahují
  implementační plán a runbooky 17/18 ze svého neměnného zdrojového commitu.

## Lokální testy

Windows: celé `go test -count=1 -timeout=120s ./...` a `go vet ./...` prošly.
Linux (Go 1.27.2, CGO/race, cached modules, offline network):
`go test -race -count=1 -timeout=120s ./...` prošel. Cílené script testy v
agent/adapter také prošly třikrát s race detectorem.

Skutečný Bash: stdout/stderr, flood, UTF-8 a truncation, exit/signal, timeout,
same-group descendants, inherited pipes, explicitní cwd/argv, čistý environment
a odmítnutí neznámé akce před exec. Saturated process slot odmítá další spuštění.
Konfigurační limity, strict/duplicate YAML, wire kompatibilita a check-config
bez exec zůstávají pokryté. Docker/Podman/systemd regresní testy prošly.

mTLS e2e: dry-run bez writes, ACK, heartbeat při floodu, žádné automatické
script/status polling, ztráta výsledku po ACK, durable completion bez controlleru,
restart obou stran a read-only reconciliation. Samostatně graceful shutdown a
tvrdý pád procesu agenta; původní side effect zůstává jeden, duplicate ID je
lookup, změněný payload je conflict, žádný automatic replay. Timeout script
status se klasifikuje `EXECUTION_UNKNOWN`, nikoli read-only `TIMEOUT`.
Audit neobsahuje raw script output.

## Debian balíčky

Jednorázové nebootované Debian 13 kontejnery:

- Agent fresh install bez Docker/Podman/uidmap a bez vytvoření service účtu.
- Upgrade skutečného preview.3 na preview.4 zachová user config/key/journal,
  legacy enable symlink a ruční user override; nepřepne na generic unit.
- Manual system-unit/mask odmítnutý bez přepsání; parser generické user/system
  a legacy Podman unit, syntax maintainer scripts, reinstall/remove/purge.
- Controller install preview.3, upgrade preview.4, downgrade, remove/purge;
  zachování privátních credentials/ledger a žádný implicitní start/restart.
- Obě architektury balíčků strukturovaně ověřeny. Arm64 nebylo spuštěno na ARM;
  parser není důkaz fungujícího bootovaného systemd ani rootless runtime.

Hosted CI pro runtime commit sleduje
[run 38465709916](https://github.com/goffamartin/homelab-control-plane/actions/runs/38465709916).
V okamžiku zápisu běží; jeho výsledek není zahrnutý do tvrzení o lokální akceptaci.

## Otevřené operátorské brány a další etapa

Na homelab VM se neprovedly žádné změny. Upgrade, ruční restart vybrané služby,
script smoke, migrace na generic user unit, reboot/linger a živé Docker/Podman
regrese musí provést operátor podle [profilů](17-generic-agent-profiles.md) a
[script runbooku](18-script-resource-contract.md). Nepřepisovat credentials,
journal/ledger nebo existing resource konfiguraci a neaktivovat dva agenty
pro stejnou identitu. Runtime balíčky po upgradu slepě neodstraňovat přes autoremove.

P04 následuje: sdílený non-destructive setup engine a read-only doctor. Setup wizard,
managed CA/join a certifikační renewal ještě nejsou implementované. Process groups
nejsou sandbox/cgroup containment; skripty nesmějí být nedůvěryhodné ani daemonizující
a nesmějí tisknout secrets.
