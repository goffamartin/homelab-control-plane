# HLCP v1 — revize a plán dokončení

Datum: 2026-10-09. Revidovaný commit: `5280f0b` na `main`.
Rozsah: všechny produkční Go balíčky, CLI, existující testovací scénáře,
návrhová dokumentace, konfigurace a deployment artefakty.
Tento dokument obsahuje zjištění a návrh implementace; opravy ještě nejsou provedené.

## 1. Závěr a cílový stav

Projekt má použitelný základ protokolu a rozdělení odpovědností, ale zatím není
dokončené MVP ani ověřené produkční řešení. Největší mezery jsou ve vazbě identity
na certifikát, životním cyklu session, skutečném ACK/result workflow, trvanlivosti
deduplikace a propojení CLI s reálným stavem prostředků. Dosavadní zelené jednotkové
testy tyto vlastnosti jako celek nepotvrzují.

Navrhovaný konečný stav **v1**:

- Jeden controller a více agentů; Go, HLCP přes TCP/TLS 1.3 a CBOR.
- Autentizovaný agent může vystupovat jen jako node určený jeho certifikátem.
- CLI obslouží discovery, detail, stav, validovaný dry-run a povolené akce.
- Docker a rootless Podman: `status`, `start`, `stop`, `restart` nad konkrétním
  nakonfigurovaným kontejnerem. Minecraft není součástí akceptace. Systemd adaptér
  zůstává vedlejší podporovanou možností, ne podmínkou prvního nasazení.
- Agent posílá pozorovaný stav, jeho stáří a změny; controller odlišuje stav nodu
  od stavu resource a od posledního výsledku příkazu.
- Přijatý příkaz dostane ACK před externí akcí; opakovaný příkaz nezpůsobí druhé
  provedení v deklarovaném retenčním okně. Nejistý výsledek zůstává explicitně nejistý.
- Reconnect, timeout a restart mají zdokumentovaný význam a automatické testy.
- Omezené fronty, zápisy, výstupy, journal, audit a retry; předvídatelné ukončení.
- Ověřená instalace controlleru i agenta na Debianu a reprodukovatelné experimenty
  odpovídající výzkumné otázce ANI-COM, včetně závěrečného reportu.

Mimo v1: webové UI, plánování úloh, obecný shell, HA/multi-controller, vzdálená
editace agent konfigurace, rozsáhlé RBAC a automatická správa celé PKI.
Nezavádět claim exactly-once; zejména restart kontejneru není bezpečné libovolně
opakovat jen proto, že jde o lifecycle akci.

## 2. Co bylo ověřeno

- Výchozí pracovní strom byl čistý.
- `go test -count=1 -cover ./...` prošel; `go vet ./...` prošel.
- Pokrytí: agent 11,2 %, controller 39,2 %, session 46,4 %, command 72,7 %;
  všechny tři `cmd/*` 0 %. Procenta jsou orientační, důležitější jsou chybějící scénáře.
- `go test -race ./...` se nespustil: `-race requires cgo`. V dostupném PATH nebyl
  gcc/clang. Výsledek race kontroly není známý; do akceptace patří Linux s CGO.
- Reprodukce: `hlcp -n debian-01 -r example-catalog` vrací pouze `hlcp: resource`.
- Reprodukce: `hlcp -n missing-node -r missing-resource -a unsupported --dry-run`
  úspěšně vytiskne payload bez přístupu ke controlleru a bez validace existence.
- Negativní bezpečnostní a souběhové nálezy níže jsou závěry z konkrétních cest kódu;
  nebyly zkoušeny proti běžícímu HomeLabu.
- Nebylo provedeno skutečné nasazení, Docker/Podman smoke, systemd ověření ani
  test pádu OS/disku. Existující journal testy simulují znovuotevření souboru,
  nikoli výpadek napájení nebo dva procesy.

## 3. Nálezy a návrhy oprav

P1 = nutné vyřešit před reálnými řídicími akcemi; P2 = funkční/provozní chyba
nebo podmínka dokončeného MVP; P3 = konzistence a udržovatelnost.
Odkazy uvádějí přesná výchozí místa v commitu `5280f0b`; po změnách se řádky posunou.

### R01 — P1: certifikát není svázán s node ID

Místo: `internal/controller/server.go:76-95`, `internal/session/session.go`
(`Accept`), `internal/transport/tls.go` (`ServerTLSConfig`).
TLS ověří certifikát proti CA, ale `HELLO.node_id` se převezme bez vazby na peer
certifikát. Držitel jiného platného agent certifikátu může deklarovat cizí node
a přepsat jeho registraci. ALPN je nakonfigurované, ale produkční cesta nekontroluje
výsledek vyjednání.

Oprava: jednoznačná identita v URI SAN nebo explicitní autorizované mapování;
porovnání s HELLO před publikováním registrace, role agent/controller a povinné
ALPN. Negativní testy cizího nodu, CA, expirace, EKU, jména serveru a ALPN.

### R02 — P1: stará session a cizí node mohou ovlivnit command tracking

Místo: `internal/controller/server.go:92-101`, `sessions.go:25-35,57-58`,
`commands.go:24-42`.
Registry a writer se mění odděleně, starý socket se nezavírá. Souběh dvou registrací
může spojit inventory jedné session s writerem druhé. Výsledek se koreluje pouze
globálním command ID, bez očekávaného nodu/session. Starší spojení dál dodává results.

Oprava: atomické zveřejnění jednoho objektu session včetně inventory a generace,
uzavření nahrazené session, ověření odesílatele výsledku proti operaci. Při legitimním
reconnectu přijmout cached výsledek od stejné autentizované identity podle explicitní
reconciliation politiky; nikoli automaticky od libovolného starého writeru.

### R03 — P1: timeout neomezuje zápis ani externí akci

Místo: `internal/session/writer.go:19-33`, `internal/controller/sessions.go:61-78`,
`internal/agent/agent.go:52-54,154-188`, `internal/adapter/systemd.go:18-20`.
Execute nejprve synchronně zapisuje bez write deadline a teprve pak čeká s contextem.
Při protistraně, která nečte, může zůstat blokovaný i po HTTP timeoutu. Writer mutex
blokuje další zprávy. Agent nemá vlastní časový limit TCP/TLS připojení a adaptéry
dostávají `context.Background()`: visící runtime může držet dispatcher neomezeně.

Oprava: kontextové/bounded fronty zápisu, write deadline, ukončení vadného socketu,
samostatné limity connect/TLS/akce a řízené ukončení subprocessu. Neinterpretovat
zabití lokálního Docker CLI jako důkaz, že daemon externí operaci neprovedl.

### R04 — P1: ACK přichází po provedení; IN_PROGRESS se tváří jako finální výsledek

Místo: `internal/session/agent_commands.go:24-31`,
`internal/command/dispatcher.go:34-58`, `internal/session/inbound_loop.go:34-37`.
Dispatcher drží globální mutex po celou externí akci. Session po jeho návratu
odesílá ACK i RESULT; běžící duplicitu v téže session tedy nemůže obsloužit.
Controller ACK pouze zahodí. Po chybě uložení výsledku zůstává journal pending;
další duplicita vrátí `IN_PROGRESS` jako terminální RESULT, přestože nic neběží.

Oprava: oddělit admission/durable start, ACK, provedení a durable result. Jedna
omezená pracovní fronta pro MVP je dostačující; reader zůstane dostupný. Pending
duplicita dostane pouze ACK. Sjednotit stavový automat obou stran a chyby journalu.

### R05 — P1: journal nemá úplnou crash/process bezpečnost

Místo: `internal/command/journal.go:40-81,110-167`.
Existující prázdný soubor se považuje za prázdný journal. Chybí schema version,
kontrola vazby result ID na command ID, limit velikosti při načítání a mez i pro
diagnostické zprávy. Mutex chrání jen jeden objekt v jednom procesu, nikoli druhého
agenta se stejnou cestou. Zápis syncuje temp soubor, ne adresář po rename; obecná
garance trvanlivosti při pádu OS tím není doložena. Dokumentace Go navíc negarantuje
atomicitu Rename na non-Unix platformách (`os/file.go:431-435` lokálního toolchainu).

Oprava: výhradní vlastnictví journalu procesem, verzovaný validovaný formát,
fail-closed poškození, Linux durable replace včetně adresářového syncu a testy
selhání jednotlivých kroků. Po chybě uložení terminálního výsledku agent nesmí
vytvářet iluzi bezpečného pokračování; přejde do explicitního chybového režimu.
Nezahazovat/nepřepisovat nečitelný journal automaticky. Formát migrovat se zálohou.

### R06 — P1: retenční počet sám nestačí pro bezpečné retry

Místo: `internal/command/journal.go:99-102,127-132`,
`internal/controller/sessions.go:66-78`.
Retence odstraňuje nejstarší ID bez ohledu na stáří nebo pending stav. Dostatečně
mnoho dalších příkazů může odstranit výsledek ještě během retry původního příkazu;
ten se pak provede znovu. Soubor je omezen počtem záznamů, ne jejich bajtovou velikostí.
Předchozí tvrzení o neomezeném nehromadění je tedy správné jen pro počet běžných
záznamů, nikoli jako úplná provozní a deduplikační garance.

Oprava: minimální chráněná doba alespoň pro podporovaný replay/retry horizont,
ochrana running/unknown záznamů, kapacitní i bajtový limit. Když bezpečné odstranění
není možné, odmítnout novou akci `JOURNAL_FULL`. Pro stará ID definovat expiraci
a chování po konci záruky; count-only garanci neprezentovat jako časovou.

### R07 — P1: lokální administrační API nemá vynucenou hranici přístupu

Místo: `cmd/hlcp-controller/main.go:19,44-45`, `internal/controller/http.go:53-55`.
Výchozí adresa je loopback, ale `-api-listen 0.0.0.0:9444` otevře neautentizované
řídicí API do sítě. Nejsou HTTP serverové timeouts, limit body, striktní JSON ani
kontrola browserových požadavků; Content-Type se neověřuje.

Oprava: v1 povolí pouze explicitní loopback IP; lokální token v souboru s omezenými
právy, host/origin pravidla, odmítnutí neautorizovaného příkazu a omezený JSON request.
Alternativou je Unix socket s filesystem ACL, ale pro první verzi ponechat schválené
loopback HTTP a doplnit jeho ochranu. Chyby vracet jako jednotný JSON kontrakt.

### R08 — P2: produkční liveness cesta neodpovídá kontraktu

Místo: `internal/session/inbound_loop.go:24-44`, `internal/controller/server.go:99-102`.
Sekvence heartbeat se zahazuje; ACK/RESULT neobnovují aktivitu. LastActivity registry
se aktualizuje jen při změně stavu, při odpojení se naopak přepíše časem odpojení.
Test opakované sekvence pokrývá starý `RunHeartbeatLoop`, který server nepoužívá.
Výchozí degraded po jediném intervalu vede při běžném jitteru k přepínání stavu.

Oprava: validace sekvence ve skutečném inbound loopu, validované zprávy obnovují
aktivitu, průběžný last_seen oddělený od disconnected_at, promyšlené prahy a testy
aktuální cesty. Nedůvěryhodná nebo nesouvisející zpráva nesmí obnovit liveness.

### R09 — P2: shutdown a reconnect nemají řízený konec práce

Místo: `internal/controller/server.go:53-66`, `internal/session/inbound.go:19-50`,
`cmd/hlcp-controller/main.go:42-55`, `internal/agent/agent.go:138-188`.
Close ukončí listener, nikoli všechny session; goroutine odesílající do plného
inbound kanálu se nemůže zrušit. HTTP Shutdown dostane již zrušený context a hlavní
proces na dokončení nečeká. Selhání API listeneru může skončit úspěšným exit kódem.
Agent resetuje backoff už po krátkém navázání; maximum s jitterem je 36, ne 30 sekund.

Oprava: jeden vlastník session contextu/socketu/goroutin, cancellable sends,
čekání na stop, fresh shutdown timeout, nenulový exit při startup failure.
Resetovat backoff až po stabilní session a jasně definovat skutečné maximum.

### R10 — P2: stav resource je z konfigurace, nikoli pozorování

Místo: `internal/agent/agent.go:110-139`, `internal/session/inbound.go:29-52`.
Synchronizace vznikne jednou z YAML a opakuje se beze změny; revision je vždy 1.
Runtime se nečte, po příkazu se stav neaktualizuje a STATE_UPDATE není přijímán.
Controller tak může zobrazovat `unknown` nebo jiný konfigurační údaj trvale.

Oprava: adapter Observe/Execute kontrakt, nový observed snapshot při každém spojení,
refresh po akci a periodicky, explicitní timestamp/freshness, verze inventory a
validace úplnosti snapshotu. Status nodu a status kontejneru nesmějí splývat.

### R11 — P2: resolver není zapojen a CLI má zavádějící úspěchy

Místo: `cmd/hlcp/main.go:35-54,82-103`, `internal/cli/parser.go:46-51,68-93`,
`internal/cli/resolver.go:38-72`.
Resource detail končí placeholderem. `-n X -a restart` spadne do node detailu místo
odmítnutí nepodporované akce. Chybějící rodič selektoru a konfliktní singular/plural
se nevalidují. Dry-run je pouze lokální tisk mapy; resolver s kontrolami se nepoužívá.
`--json` nemění výstup a neúspěšný command result v HTTP 200 stále vrací exit 0.
Po timeoutu uživatel nemá použitelnou cestu pro zjištění výsledku stejného ID.

Oprava: jeden validační kontrakt na serveru pro dry-run i execute, úplná tabulka
intencí CLI, typed API client, detail endpoint, text/JSON formátování a exit kódy;
zobrazení ID před odesláním, `--command-id` a read-only dohledání výsledku.

### R12 — P2: retry ztrácí sémantiku a historii operace

Místo: `internal/controller/sessions.go:61-78`, `internal/controller/http.go:67-83`.
Všechny send chyby se ignorují, příkaz může být odeslán i s již zrušeným contextem,
ACK nemění retry politiku. Po 30s HTTP deadline se tracker odstraní a pozdější
výsledek zanikne. Timeout přitom neznamená, že akce neproběhla. Příchozí HTTP
duplicitní ID nemá stabilní idempotentní odpověď; restart controlleru vše zapomene.

Oprava: command service s nezávislým omezeným životním cyklem operace, stav ACK,
deadline/retry budget, rozlišení přechodné a trvalé chyby, vlastnictví nodem,
výsledek dostupný podle ID. Při obnově controlleru automaticky neprovádět novou
akci pro expirovaný nebo nejistý příkaz; nejdříve read-only reconciliation.

### R13 — P2: limity protokolu a validace si odporují

Místo: `internal/protocol/payload.go:42-48,121-127,171-173`,
`internal/protocol/resource.go:9-15`, `internal/session/session.go` (`withDefaults`,
`negotiateVersion`). CBOR decoder dovoluje nejvýše 64 prvků pole, inventory validátor
256 resources: inventory s 65 položkami projde validací a selže při přenosu.
ACK/RESULT nemají sémantickou validaci, ID dovolují řídicí znaky/cesty a heartbeat
interval může při převodu do uint32 milisekund ztratit platný význam. Výběr verze
bere poslední společný prvek nabídky, ne nutně numericky nejvyšší verzi.

Oprava: společné limity wire/domény, testy hranic 64/65/256/257, syntaxe ID,
validace výsledků, ACK, číselných intervalů a verze; skutečný 18b layout zdokumentovat
tabulkou offsetů místo nynějšího zavádějícího bitového obrázku.

### R14 — P2: adaptéry a konfigurace nemají provozní ochrany

Místo: `internal/adapter/systemd.go:18-20,37-51`,
`internal/adapter/container.go:25-37`, `internal/agent/agent.go:35-59,85-105`.
CombinedOutput je neomezené, chybějící executable může vrátit prázdnou diagnostiku,
status systemd nerozlišuje běžné inactive od chyby. Cíl se nevaliduje proti syntaxi
option argumentů; allow-list nesmí umožnit rozšíření na více cílů přes lokální konfiguraci.
Neznámý adapter či nepodporovaná akce se projeví až za provozu, YAML není striktní.

Oprava: preflight konfigurace včetně všech resources, běhové závislosti a oprávnění,
bounded stdout/stderr, typed status, mapování chyb a validovaný jediný target.
Docker/Podman argumenty ověřit samostatnými testy i reálným runtime smoke testem.

### R15 — P2/P3: nasazení a dokumentace předbíhají ověření

Místo: `deploy/systemd/hlcp-agent.service:11,18-20`, `docs/03-debian-agent-deployment.md`,
`docs/01-hlcp-v1-design-draft.md:165` a závěr návrhu.
Společný unit vyžaduje docker skupinu i pro Podman, zatímco `ProtectHome=true`
není obecný funkční profil pro rootless Podman. Docker socket přístup prakticky
ruší slib silné izolace uživatele hlcp. Controller unit a PKI bootstrap/rotace chybějí.
Dokumentace stále odkládá Docker a používá Minecraft/systemd jako MVP, ačkoli uživatel
zvolil Docker/Podman. Některé deklarované typy zpráv nejsou implementovány.

Oprava: oddělené ověřené deployment profily, controller unit, certifikátový postup,
Linux line endings, reprodukovatelné buildy a aktuální matice supported/deferred.
Nepovažovat pouhou přítomnost .service souborů a zelené Go testy za test nasazení.

## 4. Architektonická rozhodnutí pro implementaci

Následující jsou doporučené cíle, nikoli tvrzení o dnešní implementaci.

1. **Identita:** URI SAN `urn:hlcp:node:<node-id>` v agent certifikátu; přesná shoda
   s HELLO. DNS SAN controlleru se ověřuje standardním TLS. Privátní CA oddělená
   od dev CA; rotace certifikátu zachová stabilní node identitu.
2. **Lokální API:** loopback HTTP s lokálním bearer tokenem, podporovaným i CLI.
   Přístup do sítě případně řešit až samostatnou verzí, ne rozšířením bind flagu.
3. **Admission:** novou mutaci přijmout jen nad připravenou online session a
   aktuálním inventory. Již přijatá operace může čekat na reconnect v rámci deadline.
   Degraded/offline stav zůstává čitelný. Agent vždy znovu ověřuje vlastní allow-list.
4. **Execution:** pro MVP omezená fronta a jeden worker na agentovi; žádný souběh
   lifecycle akcí téhož resource. Admission a reader nesmějí blokovat na executor mutexu.
5. **Výsledky:** oddělit stav operace (`accepted/running/completed/unknown`) od
   success/failure externí akce. `in_progress` není terminální RESULT. ACK není úspěch.
6. **Persistence:** zachovat malý JSON journal, pokud projde crash/locking testy.
   Přechod na embedded DB není nutný předem. Sdílet úzce vymezený durable-store
   mechanismus i pro controller ledger, nikoli obecný framework.
7. **Retence:** zavést časové okno a kapacitu společně; návrhový výchozí replay
   horizont 24 h, kapacita 1024 jako konfigurovatelná mez. Je-li všech 1024 záznamů
   chráněno, nový příkaz odmítnout. Starý formát bez timestamps migrovat konzervativně.
   Neznámé stavy řešit read-only reconcilací nebo explicitním operátorským uzavřením.
   Přesné hodnoty potvrdit z očekávané frekvence příkazů v první fázi.
8. **Runtime:** Debian/Linux je referenční agent platforma; CLI funguje i na Windows.
   Garance Linux journalu nevztahovat automaticky na Windows filesystem.
9. **Stav:** inventory revision platná jednoznačně v rámci session; změnu obsahu
   a nové session nelze zaměnit. Volbu session-scoped revision promítnout do všech
   consumers a dokumentace, nebo zavést persistovaný monotónní counter.
10. **Experiment:** TCP už zajišťuje retransmisi paketů. HLCP retry řeší nejistotu
    aplikačního výsledku/reconnect; v měření tyto dvě vrstvy vykazovat zvlášť.

## 5. Fáze implementace a commity

Pořadí níže je implementační pořadí. Každý uvedený commit má obsahovat kód, relevantní
regresní testy a změnu kontraktu/dokumentace. Fáze může mít 1–3 commity; před další
fází musí být splněna její výstupní podmínka. Žádný commit nemá záměrně rozbíjet build.

### F0 — sjednotit cílový kontrakt a evidenci

Nálezy: R15 a rozhodnutí z oddílu 4. Závislosti: žádné.

- Aktualizovat vizi, protokol a původní roadmapu: Docker/Podman jako hlavní scénář,
  matice podporovaných zpráv, rozdíl prototyp/ověřená vlastnost, garantovaný rozsah v1.
- Zapsat ADR pro identitu, lokální API, command state machine, retry/retenci,
  controller restart a session-scoped revision. Uvést nejistoty, ne tvrzení exactly-once.
- Přidat testovací pomůcky pro fake executor, deterministické hodiny/retry a dočasnou PKI.
  Reuse test helperů připravit bez změny chování služby.

Commity: `docs: define HLCP v1 completion contract`; případně
`test: add reusable session and executor fixtures`.
Hotovo: každá vlastnost v1 má konkrétní akceptační scénář a referenci na fázi.

### F1 — wire validace a startovní konfigurace

Nálezy: R13, konfigurační část R14. Závislosti: F0.

- Sjednotit CBOR limity a resource limity; validovat ACK/RESULT, ID a všechny intervaly.
- Striktní YAML, enumy adapterů/akcí, cíle, retence a cesty; neplatné hodnoty odmítnout
  před navázáním session. Doplnit `--check-config` bez spuštění resource akcí.
- Ujasnit známý vs podporovaný typ zprávy a kompatibilitu nepovinných polí.

Commity: `fix: align wire limits and payload validation`;
`feat: validate agent configuration before startup`.
Hotovo: hranice 64/65/256/257 a neplatné payloady mají test; chybná konfigurace
nezaloží online node ani neotevře execution workflow.

### F2 — identity a ochrana lokálního API

Nálezy: R01, R07. Závislosti: F1.

- Ověřit SAN identitu a ALPN před registrací. Oddělit agent/controller cert profile.
- Vynutit loopback bind, lokální token a CLI konfiguraci tokenu; neukládat token do Git.
- HTTP timeouts/body limit, JSON content type, jedno JSON tělo bez trailing hodnot,
  konzistentní error envelope, kontrola Host/Origin a bezpečné zacházení s redirecty.

Commity: `fix: bind agent identities to verified certificates`;
`feat: secure the local controller API`.
Hotovo: certifikát A nepřevezme node B; neautorizované API nespustí příkaz;
non-loopback adresa je odmítnuta; negativní TLS/HTTP testy projdou.

### F3 — session ownership, liveness a ukončení

Nálezy: R02, R03 (síť), R08, R09. Závislosti: F2.

- Zveřejňovat session/inventory/writer atomicky, přidat session generation a uzavřít
  předchozí socket. Vázat inbound events na autentizovanou identitu.
- Kontextové I/O, bounded write queue/deadline, cancellable reader dispatch,
  limit souběžných handshaků/session. Propagovat stop a čekat na goroutiny.
- Přenést heartbeat validaci do jediné produkční cesty; obnovovat last_seen při
  validní aktivitě. Odstranit nebo jasně izolovat překonaný heartbeat-only loop.
- Opravit startup error propagation, graceful HTTP shutdown a reset/max backoffu.

Commity: `fix: make session replacement and shutdown deterministic`;
`fix: bound session IO and reconnect attempts`;
`fix: enforce liveness in the active session loop`.
Hotovo: blackhole/nečtoucí peer skončí v limitu, opakovaná heartbeat sekvence se
odmítne, plný kanál nezanechá goroutinu, stará session neovlivní novou;
startup failure vrací chybu. Testy musí běžet na produkčním loopu.

### F4 — durable journal a bezpečná retence

Nálezy: R05, R06. Závislosti: F1, F3; musí předcházet async execution.

- Verzovaný schema, vazba na node identitu, kontrola záznamů, read/output byte limity.
- Process lock, explicitní Close, durable replace na Linuxu, zachování diagnostiky
  při chybě a bezpečná migrace původního souboru.
- Timestamps, minimální replay okno, maximální počet/bajty, zákaz vyřazení running
  a nevyřešených unknown. `JOURNAL_FULL` místo oslabení deduplikace.
- Po storage failure zastavit nové mutace, dokud se journal bezpečně neobnoví.

Commity: `fix: make command journal recovery fail closed`;
`feat: protect replay windows with bounded journal retention`.
Hotovo: testy chyb Write/Sync/Rename/directory Sync, corruption/truncation, dva
procesy, restart uprostřed akce, zaplnění a migrace; žádná druhá execution v okně.
Samostatně uvést, co crash testy dokazují a co závisí na filesystemu/úložišti.

### F5 — skutečný agent command lifecycle

Nálezy: R04, R03 (execution), R14 (runner). Závislosti: F3, F4.

- Oddělit přijetí a provedení: validace → durable running/admitted → ACK → worker
  → durable terminal → RESULT. Výslovně rozlišit queued vs running pro crash recovery.
- Reader obslouží duplicity i během akce; `in_progress` neukončuje operaci.
- Bounded fronta, jeden worker, timeout akce, bounded output a typed výsledky.
- Po ztrátě session přijatá akce může dokončit zápis do journalu; při shutdownu
  definovaný čas na dokončení, jinak unknown. Neztratit vlastnictví starých workerů.

Commity: `refactor: separate command admission from execution`;
`feat: bound adapter execution and preserve uncertain outcomes`.
Hotovo: pomalá akce dostane ACK před dokončením, heartbeat pokračuje, duplicita má
jedno provedení, queue-full je stabilní chyba a failure persist nezpůsobí falešné OK.

### F6 — controller command service a reconciliation

Nálezy: R02 (výsledky), R12, propojení R06. Závislosti: F2–F5.

- Operace mají node, ID, payload fingerprint, admission čas, deadline, ACK stav,
  počet pokusů a terminal/unknown výsledek. Deadline omezí i odeslání a frontu.
- Rozlišit ACK timeout a result/reconciliation timeout; parametrizovat retry
  policy pro experimenty, omezený počet pokusů a žádné retry trvalé chyby.
- Idempotentní HTTP přijetí stejného ID/payloadu, konflikt při jiném payloadu.
- Omezený durable controller ledger, pozdější výsledek uchovat i po timeoutu klienta.
  Doplnit read-only dotaz na agent journal pro návrat po restartu bez opakování akce.
- Expirace zastaví execution retry; po restartu controlleru nejprve zjistit známý
  výsledek. Unknown/expired se nesmějí tiše převést na nový příkaz.

Commity: `feat: track command acknowledgements and bounded retries`;
`feat: persist and reconcile controller command outcomes`.
Hotovo: přerušené spojení po side effectu vrátí tentýž výsledek bez druhé akce,
cizí node result se odmítne, cancel před odesláním nemutuje resource a restart
controlleru nezpůsobí automatické provedení expirované operace.

### F7 — skutečné pozorování Docker/Podman resource

Nálezy: R10, runtime část R14. Závislosti: F3, F5, F6.

- Typed observation: running/stopped/unknown/error, observed_at a diagnostika;
  refresh při connect, po akci a periodicky. Odebrat state jako zdroj pravdy z YAML.
- Přijímat aktualizace stavu s kontrolou session/revision; při ztrátě spojení stale.
- Docker a Podman status/start/stop/restart testovat fake runnerem i živým runtime.
  Vymezit standalone container; Compose stack či Kubernetes resource nejsou implicitně podporované.
- Definovat chování při chybějícím kontejneru, daemonu, executable a oprávnění.

Commity: `feat: observe and synchronize live container state`;
`test: verify Docker and Podman lifecycle adapters`.
Hotovo: externí stop kontejneru se v určeném intervalu objeví v CLI; restart session
neobnoví starou YAML hodnotu a offline node nemá stav prezentovaný jako čerstvý.

### F8 — dokončit API a CLI kontrakt

Nálezy: R11 a user-facing část R12. Závislosti: F2, F6, F7.

- Jeden serverový resolver pro execute/dry-run, kontrola capabilities a inventory;
  přesunout doménové rozhodování z CLI závislého na controller registry.
- Implementovat resource detail, validovaný dry-run, status/read-only result lookup.
- Zachovat schválené krátké optional selektory a explicitní long singular/plural;
  odmítat konflikty a chybějící rodiče, nepodporovaná node akce je chyba.
- Textové tabulky defaultně, stabilní JSON na `--json`, stderr vs stdout, exit kódy
  pro invalid request, failed action, timeout/unknown a connection error.
- `--command-id`, výpis ID před odesláním a doložitelná cesta dohledání výsledku.
  Help a příklady musí popisovat skutečné chování, včetně nejistoty při timeoutu.

Commity: `feat: unify command validation and discovery endpoints`;
`feat: complete progressive CLI navigation and output contracts`.
Hotovo: test celého schváleného příkazového slovníku; dry-run neexistující resource
selže a neposílá COMMAND; failed action vrací nenulový exit; detail není placeholder.

### F9 — integrační brána a build/release kvalita

Nálezy: chybějící důkaz vlastností R01–R14. Závislosti: F1–F8.

- E2E: skutečné CLI + HTTP API + controller + agent + dočasná mTLS PKI + fake executor
  s počitadlem side effectů. Reálné Docker/Podman smoke jako oddělená Linux sada.
- Fault injection: rozpojení před ACK, po ACK, po side effectu, před RESULT, během
  journal update; restart obou procesů; cizí/stará session; paralelní klienti.
- Linux `go test -race`, relevantní fuzz testy frame/payload/parser/journal decode,
  pozorování leaků po opakovaných reconnectech, 100+ příkazů pro ACK backpressure.
- CI a reproducible build tří binárek, formátování, vet, testy, verzování a checksumy;
  ověřit používané závislosti včetně YAML RC a bezpečnostní aktualizace v době implementace.

Commity: `test: cover command recovery across process and network failures`;
`ci: verify and build HLCP release artifacts`.
Hotovo: všechny kritické failure scénáře mají automatický zelený test. Pokrytí
nenahrazuje akceptaci; není potřeba honit libovolné procento ani pouštět drahé CI při každém drobném kroku.

### F10 — provozní pozorovatelnost a nasazení

Nálezy: R15, chybějící audit/metriky. Závislosti: F9.

- Audit admission/ACK/result/unknown, identity, node, resource, ID, pokusy a trvání;
  strukturované logy bez klíčů/tokenů, omezená retence přes journald nebo rotation.
- Counters pro frames/bytes, retries, reconnects, queue/journal capacity a liveness
  transition; bez nutnosti nové veřejné metrické služby.
- Controller systemd unit, agent base unit bez docker skupiny; explicitní Docker
  override a samostatně ověřený rootless Podman user-service profil.
- PKI bootstrap dev/production, SAN/EKU, distribuce secretů, expiry/rotation/revoke
  runbook, WireGuard bind/firewall, konfigurace tokenu a systemd StateDirectory.
- Linux LF pro deployment soubory, build/install/upgrade/rollback a journal migrace.

Commity: `feat: expose bounded audit and experiment telemetry`;
`build: package verified Debian controller and agent profiles`.
Hotovo: `systemd-analyze verify`, instalace na čistém Debianu, reboot, cert rotation,
upgrade/rollback a Docker/Podman smoke jsou doložené. Teprve tato brána znamená
připravenost pro zkušební HomeLab nasazení; konkrétní hosty/certifikáty určí operátor.

### F11 — experimenty a konečné předání ANI-COM

Závislosti: F9, F10. Řídí se původní výzkumnou otázkou.

- Izolovaná Linux test topologie/netns; netem scénáře s automatickým cleanupem
  a bez zásahů do aktivního management rozhraní.
- Matice H = 1/5/10 s, ztráta 0/1/5/10 %, delay 0/50/200 ms a nejméně dvě
  zdokumentované HLCP retry politiky. Pilot určí počet opakování a délku běhů.
- Uchovat build SHA, config, seed, runtime verze, časové značky a raw výsledky.
  Měřit latency/timeout/unknown/failure odděleně, p50/p95 a detekční zpoždění,
  false positives, reconnecty, HLCP retries a přenosovou režii.
- Oddělit ztrátu síťového paketu (řeší TCP) od přerušení session a ztráty
  aplikačního potvrzení. Nevyhodnocovat pouze úspěšné operace.
- Report, interpretace, omezení, reprodukční návod a prezentace; zkontrolovat
  aktuální zadání kurzu z read-only `course/` před finalizací odevzdání.

Commity: `test: add reproducible network impairment experiments`;
`docs: report HLCP reliability and overhead results`.
Hotovo: druhý člověk zopakuje scénář a závěry jsou doložené měřením, nikoli odhadem.

## 6. Git workflow a průběžná akceptace

- Tato revize je samostatný dokumentační výstup; nemíchat ji s opravami runtime.
- Pro každou fázi krátká větev `codex/<phase-name>` z aktuálního `main`.
- Používat výše navržené popisné zprávy, bez HAVIT/AB prefixů (osobní projekt).
- Commitovat pouze související změny; oprava + regresní test + příslušná dokumentace
  tvoří jeden logický celek. Neprovádět plošné přejmenování během opravy spolehlivosti.
- Mezi fázemi zachovat čitelné commity; nevytvořit jeden megacommit celé implementace.
- Minimum každé fáze: relevantní testy, úplné `go test ./...`, `go vet ./...`, kontrola
  diffu a zdokumentované omezení ověření. Concurrency/crash změny navíc Linux/race.
- Protokolové změny kontrolovat agent/controller společně; před releasem měnit
  draft v1 koordinovaně, po vydání dodržet explicitní kompatibilitu/verzování.
- Aktualizovat tabulku dokončení po každé fázi a uvádět SHA, důkaz testů a zbývající
  omezení. Zelený unit test sám o sobě neuzavírá deployment ani experimentální fázi.

## 7. Milníky a definice hotovo

| Milník | Fáze | Výsledek |
|---|---|---|
| Bezpečný komunikační základ | F0–F3 | Identita, API hranice, session a liveness mají ověřené failure cesty. |
| Spolehlivý command lifecycle | F4–F6 | Durable admission, včasný ACK, bounded retry a dohledatelný výsledek. |
| Funkční produktové MVP | F7–F9 | Živý stav Docker/Podman, celé CLI a testované end-to-end recovery. |
| Provozně ověřené v1 | F10 | Debian instalace/reboot/upgrade a sledovatelný provoz. |
| Dokončený semestrální projekt | F11 | Reprodukovatelná měření, report a prezentace. |

Stav realizace:

| Fáze | Stav | Důkaz |
|---|---|---|
| F0 | Dokončeno | Accepted ADR 0001–0004, `05-v1-acceptance.md` a sjednocené projektové dokumenty |
| F1 | Dokončeno | Sjednocené wire limity, sémantická validace payloadů, striktní YAML a `--check-config` |
| F2 | Dokončeno | Certifikátová URI SAN identita, povinné ALPN, negativní TLS testy; autorizované loopback API, omezený JSON a CLI token/redirect test |
| F3 | Dokončeno | Atomická session/inventory, bounded I/O a spojení, ordered inbound/liveness, korelace node výsledků, řízený shutdown; regresní testy a Linux `go test -race` |
| F4 | Dokončeno | Verzovaný node-bound journal, OS zámek, durable Linux replace, migrace se zálohou, 24h replay ochrana a limity; fault injection, dva procesy, abrupt exit a Linux race testy |
| F5 | Dokončeno | Durable queued → ACK → running → terminal, bounded single worker, reconnect deduplikace, rušení a omezený výstup; Windows test/vet a Linux race |
| F6 | Dokončeno | Verzovaný bounded controller ledger se zámkem, durable admission/ACK/result, HTTP idempotence a pozdní výsledky; query-only reconnect/restart i po expiraci. Fault injection, abrupt exit, wire recovery, Windows test/vet a Linux race |
| F7 | Rozpracováno | Typed runtime observation, connect/post-action/periodic refresh, session/revision validace a freshness; Windows test/vet, Linux race, fake Docker/Podman a živý Docker lifecycle/externí stop. Zbývá živé ověření Podman, který v této stanici není dostupný |
| F8 | Dokončeno | Společný serverový resolver execute/dry-run, inventory/capabilities, resource detail; striktní progressive selektory, text/JSON výstup, exit kódy, stable command-ID a read-only lookup. Testy skutečného CLI procesu, Windows test/vet a Linux race |
| F9 | Rozpracováno | Skutečné CLI/mTLS durable recovery; 128 duplicit + 128 excess při plné frontě, 64 reconnectů s explicitním join, bounded frame/CBOR/CLI/journal fuzz kampaně. Windows test/vet a Linux race; zbývá CI/build/release a dependency/security brána |
| F10–F11 | Plánováno | Kritéria jednotlivých fází výše |

F7 má implementovaný observation/synchronization blok; zbývá živý Podman smoke
na dostupném Podman hostu. F8 API/CLI kontrakt je implementovaný nezávisle na této
runtime bráně. F9 má první end-to-end recovery blok; `06-recovery-test-evidence.md`
odděluje doložené recovery/stress/fuzz scénáře od zbývající CI/build/release brány.
Není důvod přepisovat celý projekt: framing,
základní TLS setup, datové modely a adapter boundary lze zachovat a postupně zpevnit.
