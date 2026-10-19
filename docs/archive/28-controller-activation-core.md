> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05f — Controller service activation core

Interní jádro od 2026-10-14, zatím bez native systemd adaptéru a CLI příkazu.
Nejde o hotové spuštění služby ani nový release. Imported setup nadále připravuje
jen soubory; uživatelské CLI provisioning a VM acceptance jsou otevřené.
Navazující [P05g](29-controller-activation-cli.md) přidává native Debian adapter a CLI;
tento dokument popisuje samostatný core kontrakt.

## Oddělené potvrzení

`PlanControllerActivation` vytváří read-only plán nad důvěryhodným host adaptérem.
Žádný shell, libovolný unit name, executable path, scope ani run-as argument.
Adapter smí pracovat pouze s instalovanou `hlcp-controller.service`.

- Neaktivní služba: plán `start`.
- Aktivní služba: žádný restart, žádný start.
- Výslovně požadované trvalé enable: pokud chybí, plán `enable` před `start`.
- Již enabled: žádné opakované enable.
- Failed, transitional, masked, runtime-only nebo neznámý stav: odmítnutí,
  nikoli automatický reset, oprava nebo restart.

`Apply` vyžaduje samostatný consent. Před první změnou znovu porovná celý snapshot.
Změna konfigurace, account/unit/binary evidence, active nebo enabled stavu znamená
stale plan bez mutace. Plan je po přijetí snapshotu jednorázový; kopie sdílejí stav.
Preview/report uvádí pouze pevné názvy akcí, ne privátní evidence.

## Povinný kontrakt budoucího host adaptéru

`Inspect` není pouhý dotaz na ActiveState. Musí pokaždé read-only ověřit skutečný
instalovaný profil: důvěryhodné binárky a unit včetně cest/ownership, nečekané
overrides/drop-ins, OS service account a primary IDs, bezpečné konfigurační soubory,
TLS platnost/key-match/chain/role/name, policy/tokeny/admission a podporovaný stav
systemd. Nemění účty, neotevírá ledger, negeneruje credentials ani listener.
Vrátí neprázdný SHA-256 proof těchto snapshotů, bez dynamického active/enabled
stavu. CLI nesmí proof ani validaci přijímat od uživatele jako důvěryhodnou evidence.

`Change` smí provést pouze fixed enable/start přes důvěryhodný systémový nástroj,
s omezeným časem/výstupem, sanitizovaným prostředím a cancellation. Žádné
automatické enable při startu, restart, stop, reset-failed ani daemon-reload.
Native adapter musí runtime-only/masked/unknown stav odmítnout před vytvořením
snapshotu; boolean Enabled sám tyto stavy nevyjadřuje.

Balíček již vytváří účet controlleru. Tato vrstva ho nepředefinuje ani nerecykluje
cizí účet. Nedůvěryhodný či chybějící účet má vést k jasnému odmítnutí.

## Částečný výsledek není souborová transakce

Po každé akci se znovu ověří evidence a očekávaný systémový stav. Teprve potom
se akce přidá do `completed`. Selhání akce nebo následné inspekce znamená
`uncertain: true`: služba již mohla být enabled/started. Další akce se neprovede,
neproběhne retry, automatické disable/stop ani rollback konfigurace.

Cancellation před další akcí zachová již ověřené `completed`; není příkazem
vrátit změny. Cancellation během akce či neověřeného post-check vyžaduje inspekci.
Předchozí successfully enabled zůstane enabled, i když následný start selže.
Nový pokus musí vytvořit nový plán ze skutečného aktuálního stavu.

Kontrola před akcí a systemd příkaz nejsou jedna atomická operace. Budoucí adapter
musí tuto race hranici výslovně dokumentovat; core nenahrazuje OS trust checks
a negarantuje zámek proti souběžnému root správci.

## Evidence a návaznost

Deterministické host testy ověřují consent, redakci a kopie, pořadí enable/start,
active no-op, start bez implicitního enable, neplatnou evidence/stavy, stale
snapshots, částečné selhání, post-check failure, cancellation a zákaz retry.
Windows full test/vet a Linux full race/test/vet prošly. Nejsou to live
systemd/booted VM testy.

Navazuje native controller preflight/activation CLI, samostatný bezpečný user CLI
credential provisioning, agent system/user service integrace a VM reboot gate.
P05/checkpoint B ani managed PKI tímto nejsou dokončené; preview.4 se nemění.
