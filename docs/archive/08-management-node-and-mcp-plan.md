> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# Debian management node a MCP — implementační plán

Datum: 2026-10-12. Stav: **F10A–F10D připraveno a lokálně ověřeno; skutečný deployment F10E a MCP zbývají**.

Důkazy a omezení F10A shrnuje [telemetry dokumentace](../reference/09-telemetry-and-audit.md).
Journald profily jsou připravené, nikoli nainstalované; skutečné deployment brány
F10D profily a runbook shrnuje [Debian deployment](12-debian-management-deployment.md).
Skutečné deployment brány F10E zůstávají otevřené.
F10B kontrakt a důkazy shrnuje [scoped API dokumentace](../reference/10-scoped-api-credentials.md).
F10C kontrakt, limity a důkazy shrnuje [diagnostické API](../reference/11-bounded-diagnostics-api.md).

Navazuje na [F10–F11](04-review-and-completion-plan.md),
[v1 akceptaci](05-v1-acceptance.md) a [build bránu](07-build-and-release.md).
Schválený směr: jeden lehký Debian pro trvale běžící controller a MCP server,
později lehké webové UI. Detailní bezpečnostní kontrakt shrnuje přijaté
[ADR-0005](../adr/0005-management-node-and-api-adapters.md).

## 1. Cílový stav a hranice

- Jeden management VM: nativní Go služby `hlcp-controller` a nový `hlcp-mcp`
  pod systemd; později webový backend. Nezavádíme Kubernetes ani databázový cluster.
- Agenti navazují outbound mTLS spojení na stabilní DNS/IP controlleru.
  Není to automatické vyhledávání libovolného PC v LAN. Výpadek controlleru
  nezastaví kontejnery; řízení a čerstvá diagnostika během něj nejsou dostupné.
- CLI, MCP a budoucí web používají společné controller API. MCP nesmí obcházet
  resolver, autorizaci nebo ledger přímým přístupem k agentům či Docker socketu.
- Controller API zůstává na loopbacku. CLI mimo VM může používat SSH tunel.
  Agentní mTLS listener je dostupný jen z management sítě/VPN podle firewallu.
- MCP začíná read-only: inventář, pozorovaný stav, výsledky operací,
  omezené logové výřezy a metriky. Žádný obecný shell, libovolná cesta k souboru,
  vzdálená změna agent konfigurace ani automatické provádění doporučení z logů.
- Web a mutační MCP jsou následné rozšíření, ne podmínka dokončení ANI-COM.
  F11 musí pořád odpovídat původní výzkumné otázce, ne dokazovat kvalitu dashboardu.

Controller vlastní ledger. Agenti vlastní své command journals. MCP nebude mít
přístup k těmto souborům ani si vést druhou historii vykonaných operací.
Trvalé systémové logy mají diskový limit a retenci; živé diagnostické buffery,
dotazy a jejich souběh mají pevné limity. Nevzniká neomezené úložiště všech logů.

## 2. Pořadí a commitovatelné bloky

| Blok | Závislosti | Výsledek |
|---|---|---|
| F10A | F9 | Bounded audit a provozní metriky |
| F10B | F10A | Oddělené identity a serverová read-only oprávnění |
| F10C | F10B | Omezené API centrální diagnostiky |
| F10D | F10A–C | Debian profily a provozní runbooky |
| M1 | F10B–D | Read-only MCP nad controller API |
| M2 | M1 | Capability-gated čtení vzdálených container logů |
| F10E | F10D, M1; M2 pro vzdálené logy | Ověřený management VM a skutečné klientské připojení |
| F11 | Provozně ověřený základ F10 | Experimenty, výsledky a odevzdání ANI-COM |
| M3 | M1, F10E; nové schválení rozsahu | Řízené mutační MCP operace |
| W1 | M1, F10E; M3 pouze pro mutace | Lehké webové UI |

F10A–D a M1 lze připravit a integračně testovat lokálně. F10E vyžaduje skutečný
host a oprávnění k nasazení. M2 nemusí blokovat první read-only MCP s centrálními
logy, ale bez něj nesmíme tvrdit, že máme vzdálené Docker/Podman logy.
F11 nečeká na M3 ani W1. Živý Podman smoke F7 zůstává samostatnou otevřenou bránou.

Každý blok má vlastní krátkou větev `codex/<phase-name>`. Implementace, regresní
testy a aktualizace příslušného kontraktu tvoří jeden logický commit; větší blok
lze rozdělit na kontrakt/implementaci a integraci/balení. Nemíchat deployment
důkazy s neověřenou změnou runtime. Zprávy commitů jsou popisné, bez HAVIT prefixů.

## 3. F10A — audit, metriky a bounded retence

- Strukturované události: autentizace, session/reconnect, admission, ACK, result,
  timeout/unknown a odmítnutí; actor ID, node/resource ID, command ID, pokus a trvání.
- Nezapisovat tokeny, privátní klíče, celé HTTP hlavičky ani neomezené payloady.
  Auditní události mají pevné kódy a omezená textová pole.
- Čítače frames/bytes, retries, reconnects, odmítnutí, zaplnění front a journalu,
  liveness a trvání operací. Omezit počet labelů; command ID nesmí vytvářet
  neomezenou kardinalitu metrik. Zatím bez veřejného metrics listeneru.
- Journald/disková retence a in-memory diagnostika mají explicitní rozpočet.
  Audit není náhradou durable ledgeru. Chování při nedostupném logování musí být
  zdokumentované, bez nechtěného zablokování celého control plane.

Akceptace: test všech lifecycle cest, redakce citlivých polí, limity při burstu,
restart/reset metrik a selhání log sinku. Zátěžové testy prokážou, že diagnostika
nebrzdí heartbeat/ACK a nespotřebovává neomezenou paměť.

Commit: `feat: add bounded control-plane audit and telemetry`.

## 4. F10B — skutečná read-only autorizace

- Nahradit jedinou sdílenou admin identitu explicitními principals:
  minimálně observer a operator, s oprávněními a případným node/resource allowlistem.
  Stávající CLI admin token může mít explicitní kompatibilní migrační profil.
- MCP dostane vlastní observer credential, ne kopii admin tokenu. Oprávnění
  kontroluje controller na každém endpointu, včetně detailů, listů, výsledků a logů.
  Skryté MCP nástroje nebo jejich anotace nejsou bezpečnostní hranice.
- Autorizace předchází admission i odeslání COMMAND. Identita z ověřené credential,
  nikoli z neověřených `X-User`/`X-Role` hlaviček či parametrů požadavku.
- Zachovat loopback bind, Host/Origin kontrolu, timeouty a velikostní limity ADR-0002.
  Přihlašování k MCP a credential MCP → controller jsou dvě oddělené hranice.
- Rozsah není velký IAM/RBAC produkt. Definovat malý testovatelný permission model,
  fail-safe načtení konfigurace, rotaci a okamžik účinnosti odebrání přístupu.

Akceptace: observer nesmí změnit resource ani vložit ledger record; negativní test
každého mutačního endpointu a každý způsob obcházení scope. Žádný únik nepovoleného
inventáře/výsledků. CLI regrese, špatné/odebrané credentials a atomická změna policy.

Commit: `feat: enforce scoped controller API credentials`.

## 5. F10C — centrální diagnostické API

- Read-only snapshots metrik a omezený výřez controller audit/log událostí.
  Controller nebude potřebovat obecná práva číst systémový journal všech služeb.
- Odpověď obsahuje čas pozorování, cursor, truncation/gap a případný reset po
  restartu. Retence je omezená; cursor neslibuje nekonečnou ani bezeztrátovou historii.
- Návrhové počáteční limity: 200 záznamů, 64 KiB na odpověď, 5 s deadline.
  Konkrétní hodnoty potvrdit testem a zapsat do kontraktu; omezit také souběh,
  rate a celkový byte budget na principal, ne pouze jeden dotaz.
- Centrální controller logy jasně odlišit od vzdálených agent/container logů.
  Pro první verzi konečné dotazy a cursor polling, ne nekonečný stream.
- Logy jsou nedůvěryhodná a potenciálně citlivá data. Řízená redakce vlastních
  polí nezaručuje odstranění všech tajemství z libovolného aplikačního logu.

Akceptace: limity, neplatný/expirující cursor, restart, truncation, autorizace,
zrušení dotazu a souběh bez blokování command lifecycle. Případné CLI rozšíření
nejprve popsat v kontraktu, zachovat existující selector/help/JSON/exit-code chování.

Commit: `feat: expose bounded diagnostic snapshots and log queries`.

## 6. F10D — Debian balení a provoz

- Nativní systemd profily pro controller a agenta; MCP profil dokončit s M1.
  Oddělené service accounts, StateDirectory, bezpečné credentials, minimální práva
  a LF soubory. MCP nemá právo zapisovat controller ledger ani číst agent journal.
- Agentní Docker přístup je explicitní root-equivalent opt-in. Rootless Podman
  dostane user-service profil; případný linger musí povolit operátor.
  Management VM nepotřebuje Docker socket kvůli MCP.
- Runbook: ověřený release/checksumy, install, konfigurace, reboot, update,
  kompatibilita journal/ledger formátů, backup/restore a rollback. Obnova starého
  ledgeru může zapomenout operace; nesmí vést k jejich slepému opakování.
- PKI: controller SAN/server name, agent identita, CA trust, expiry, rotace a
  odebrání uzlu. Neoznačovat CRL/OCSP či hot reload za existující schopnost.
  Odebrání identity musí odmítnout nové session a uzavřít aktivní session;
  rotace restartem je přípustná, pokud je zdokumentované přerušení.
- Síťové profily: interní API loopback, agentní port jen management síť/VPN,
  MCP první privátní profil přes tunel/gateway. Žádné automatické vystavení do internetu.

Akceptace přípravy: `systemd-analyze verify`, config validace, negativní credential
testy a reprodukovatelný balíček. Skutečné install/reboot/upgrade důkazy patří do F10E.

Commit: `build: package least-privilege Debian management services`.

## 7. M1 — read-only MCP adaptér

- Nová Go binárka `hlcp-mcp`, oficiální MCP SDK vybrané a připnuté při implementaci.
  Adaptér volá pouze controller API; žádná vlastní pravidla pro výběr kontejneru.
- Počáteční nástroje: `list_nodes`, `get_node`, `list_resources`, `get_resource`,
  `list_actions`, `get_command_result`, `read_controller_logs`, `get_metrics`.
  Stav je pozorovaný snapshot s freshness, ne garance aktuálního běhu.
  Read-only nástroj nesmí jako skrytý „refresh“ vyvolat mutační COMMAND.
- Pro hostování navrhnout Streamable HTTP na loopbacku za zvoleným privátním
  přístupovým profilem; stdio lze doplnit pro lokální klienty. Před dokončením
  transportu ověřit konkrétní klient, jeho auth podporu a síťovou dosažitelnost.
- HTTP endpoint vyžaduje vlastní autentizaci, Origin/Host validaci, timeouty,
  velikostní a souběhové limity. Privátní bootstrap s bearer credential není
  automaticky plnohodnotná implementace MCP OAuth autorizace. Pokud klient
  vyžaduje OAuth, zvolit kompatibilní gateway/provider místo vlastního improvizovaného IdP.
- MCP annotace deklarují read-only charakter, ale ochranu zajišťuje F10B.
  Log obsah nesmí být interpretován jako instrukce či důvod k automatické akci.
- Rozšířit release/CI o čtvrtou binárku, všechna podporovaná sestavení, manifest,
  checksumy a `--version`. Nevydávat MCP jako neověřený ruční doplněk artefaktu.

Akceptace: SDK/transport integration, initialize/version negotiation, tool schemas,
auth/Origin negativní testy, limity a disconnect. End-to-end observer → API test
dokazuje nulovou možnost mutací i při ručním HTTP požadavku mimo MCP tool list.
Konkrétní klient musí umět navázat spojení a přečíst skutečný node.
Samotné spuštění serveru automaticky nezpřístupní HomeLab tomuto ani cloudovému klientovi.

Commit: `feat: add read-only HLCP MCP adapter`.

## 8. M2 — vzdálené Docker/Podman logy

- Nová typed capability, například `resource.logs.read`, s koordinovaným agent /
  controller kontraktem. Starý agent vrací unsupported; neposílat mu neznámé wire zprávy.
- Request vybírá pouze známý resource ID a omezené časové okno/cursor/limity.
  Žádné libovolné cesty, unit names, shell, Docker argv nebo neomezený `follow`.
- Read request má vlastní korelaci a deadline, není mutační COMMAND ani záznam
  v command ledgeru. Oddělit jeho souběh od execution workeru; prioritizovat heartbeat
  a ACK, omezit počet a velikost odpovědí, podporovat cancellation.
- Provider mapuje resource na již allowlistovaný container. Vyjádřit rotaci,
  výměnu container identity, truncation a mezery; polling nemusí být bezeztrátový.
  Raw logy mohou obsahovat tajemství: přístup jen v oprávněném scope a žádné
  neomezené kopírování do controller persistence.
- MCP `read_resource_logs` použije totéž API. První „live log“ znamená omezený
  cursor polling. Dlouhý stream a jeho backpressure jsou případná samostatná etapa.

Akceptace: fake providers + skutečný Docker a rootless Podman, timeout/cancel,
old-agent compatibility, scope, rotace a výměna containeru, velký výstup i log flood
současně s příkazy/reconnectem. Živý Podman nelze nahradit fake testem.

Commit: `feat: add capability-gated resource log queries`.

## 9. F10E — skutečné nasazení a provozní akceptace

Operátor nejprve určí VM, Debian verzi, DNS/IP, síťový profil, certifikační autoritu
a skutečné Docker/Podman testovací uzly. Bez tohoto vstupu nepřipravujeme vzdálené
zásahy, firewall změny ani generování produkčních credentials.

- Čistá instalace, kontrola bind adres/práv, připojení agentů, reboot/reconnect,
  credential/cert rotace a odebrání aktivního uzlu.
- Upgrade/rollback se zachováním ledgeru a bez opakovaných side effects;
  test diagnostických limitů, zaplněného disku a bounded retence.
- Read-only MCP přes skutečně zvoleného klienta: inventář, freshness, operace,
  centrální logy a po M2 vzdálené logy. Negativní mutační test s observer credential.
- Docker a rootless Podman smoke na neprodukčních allowlistovaných resources.
  Zaznamenat SHA, verze, konfiguraci bez tajemství a výsledky; tím lze uzavřít F7.
- Změřit RAM/CPU/disk pod očekávaným počtem uzlů a při floodu. „Lightweight“ je
  měřený rozpočet, ne nynější neověřený příslib konkrétní velikosti VM.

Výsledek: deployment evidence a jasný seznam případných otevřených bran.
Core F10 a MCP extension mají samostatnou akceptaci; chybějící Podman/klientské
připojení se označí jako neověřené, nikoli dokončené. Credentials se necommitují.

Commit: `docs: record verified Debian management deployment`.

## 10. M3 a W1 — pozdější rozšíření

M3 není součást prvního read-only nasazení. Před jeho implementací schválit scope:
allowlist resource/action, operator principal a důvěryhodný approval mechanismus.
Approval musí být vynucen na serveru a vázán na actor, node, resource, action,
command ID, expiraci a relevantní inventory/session revizi. MCP si jej nesmí sám
vystavit. Tool confirmation ani dry-run samy nejsou oprávnění. Atomicky ošetřit
souběh/spotřebování grantu; při timeoutu hledat původní ID, nevytvářet novou operaci.
Testy musí prokázat odmítnutí replay/změněného payloadu a zachování ledger sémantiky.
Commit: `feat: authorize approved MCP resource actions`.

W1 začne read-only dashboardem nad stejným API: nodes/resources/freshness,
operation results a bounded log polling. Go backend/BFF drží credentials mimo
prohlížeč; nezpřístupňuje interní API ani admin token. Login, zabezpečené cookies,
CSRF a renderování logů jako text, nikoli HTML. Frontend framework zvolíme až
při návrhu UI. Mutace následně používají stejnou autorizaci/approval jako M3,
ne další paralelní bezpečnostní engine. Commit: `feat: add management web dashboard`.

## 11. F11 a průběžné ověřování

Zachovat původní experimentální matici a reprodukční kritéria F11. Základní
měření dělat bez MCP/UI log pollingu, nebo jej explicitně evidovat jako další
faktor; neporovnávat směsi neznámé diagnostické zátěže. Rozšíření nesmí oddálit
vyhodnocení protokolu jen kvůli budoucímu dashboardu.

Runtime blok: cílené regrese, celé `go test ./...`, `go vet ./...`, Linux race
pro souběh a změny lifecycle, podle povahy fuzz/protocol compatibility testy.
Release změny navíc reprodukovatelnost a checksum/version smoke. Aktualizovat
stav fáze až s konkrétním důkazem. Dokumentační blok: odkazy a `git diff --check`.

Aktuální onboarding prioritu určuje
[plug-and-play implementační plán](16-plug-and-play-implementation-plan.md),
který začíná obecným agentem a skriptovými adaptéry. M1 — read-only MCP zůstává
samostatným navazujícím streamem; není součástí tohoto onboarding plánu.
Volba skutečného MCP klienta/auth profilu je nutná před uzavřením M1; volba hostů
a přístupů před F10E. Mutační oprávnění odložíme do samostatně schválené M3.

## Referenční kontrakty

Při implementaci ověřit aktuální kompatibilitu SDK/klienta. Tento návrh používá
jako explicitní referenční snapshot MCP specifikaci 2025-11-25:
[transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
a [authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization).
Streamable HTTP a stdio jsou transporty, nikoli záruka síťové dostupnosti či
oprávnění. Standardní HTTP autorizace má vlastní OAuth kontrakt; privátní
bootstrap musí být popsaný jako omezený profil, ne jako neověřená kompatibilita
se všemi MCP klienty.
