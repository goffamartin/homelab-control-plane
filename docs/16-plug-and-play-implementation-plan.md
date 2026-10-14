# Plug-and-play onboarding a obecný agent — implementační plán

Datum: 2026-10-13. Výchozí release: v0.1.0-preview.3, SHA
9f82d7d52139008be6cc3508f1db662c460bf14d. Plán navazuje na
[odsouhlasené návrhové poznámky](15-onboarding-and-agent-profile-design-notes.md).
Tento dokument definuje implementační etapy; navrhované příkazy ještě neexistují.
Zápis plánu není souhlas s automatickými zásahy do VM nebo zveřejněním repozitáře.

## Cíl a hranice první verze

Nový uživatel na Debianu nainstaluje balíček, projde krátkým setupem controlleru,
spáruje agenta jednorázovou pozvánkou a vybere resources. Nemusí psát YAML,
používat OpenSSL ani přenášet CSR/certifikáty ručně. Konfigurace zůstává čitelná
a ručně upravitelná. Aktualizace a opakovaný setup zachovávají identity a stav.

- Debian 12/13, Linux amd64/arm64; první živá akceptace Debian 13 amd64.
- Jeden Go agent; volitelné adaptéry Docker, Podman, systemd a lokální script.
- Uživatelská nebo systémová služba podle účtu, nikoli další HLCP role/profily ACL.
- Certifikáty: managed výchozí režim, nebo imported režim s externí obnovou.
- Žádné libovolné vzdálené shellové příkazy, přenos skriptů, automatické sudo,
  automatické zpřístupnění objevených resources nebo automatické přijetí LAN node.
- Neměnit HLCP command-ID/deduplication/unknown-result sémantiku ani dělat z něj
  scheduler. Skript může mít libovolné vedlejší účinky; allowlist není sandbox.
- Web UI/MCP, typované parametry skriptů, síťové autodiscovery, cloud účty,
  Kubernetes a plná multiplatformní správa služeb nejsou součást této dodávky.
- Rootful Podman není automaticky doporučená cesta; nejprve rootless Podman.
- Veřejné použití návrh podporuje, ale repozitář zůstává privátní do samostatného
  rozhodnutí vlastníka. Soukromé stahování nesmí vyžadovat kopírování GitHub PAT.

## Ověřený současný základ a mezery

CLI má user cli.json, configure/token-stdin a explicitní transport overrides.
Controller i rootless Podman agent mají deterministické .deb balíčky. Operátor
ověřil mTLS, controller reboot, agent reconnect, Podman status/restart a přechod
agenta na balíček. To neprokazuje všechny runtime/OS kombinace ani nové funkce.

Agent binárka není závislá na Podmanu, ale jeho .deb dnes povinně instaluje Podman
a user unit je pojmenovaná hlcp-agent-podman. Docker/systemd adaptéry již existují.
Protokol dovoluje vlastní action/type identifikátory, ale agent lokálně omezuje
akce a adaptéry a controller mapuje capability podle resource typu. Script tedy
vyžaduje změny validace, resolveru, dispatcheru a observations, ne jen nový YAML.
Runner omezuje výstup, ale ukončení potomků musí být pro skripty dopracované.
Runtime mTLS dnes vyžaduje hotový certifikát; API 9444 je loopback-only.
Allowlist a TLS credentials se načítají při startu, automatický issuer/renewal chybí.

## Navržená výsledná cesta uživatele

1. Stáhnout ověřený balíček controlleru a nainstalovat přes APT.
2. `sudo hlcp-controller setup`: adresa/DNS, managed/imported TLS, uživatel CLI,
   potvrzení service start/boot a bezpečná záloha. Po setupu funguje `hlcp -n`.
3. `hlcp pairing create --node debian-01`: jednorázová pozvánka.
4. Na druhém stroji nainstalovat obecného agenta; `hlcp-agent join` jako skutečný
   runtime vlastník, vložit pozvánku do skrytého vstupu, potvrdit službu/linger.
5. `hlcp-agent resources configure`: lokálně vybrat cíle a pojmenované akce.
6. `hlcp doctor` / `hlcp-agent doctor`: přehled připravenosti bez mutací/tajemství.
7. Explicitní ověřená aktualizace přes helper; certifikáty managed režimu obnovuje
   služba sama. Expirace/odebrání node nikdy neobchází TLS.

Imported režim místo managed join poskytne agent setup s hotovým certifikátem,
klíčem a trust bundlem; node správce výslovně povolí. Hotové certifikáty nedávají
HLCP možnost podepisovat jiné. Automatické podepisování přes externí PKI není MVP.

## Implementační pravidla pro všechny etapy

Stav při vytvoření plánu: P01–P12 jsou neimplementované. Existující preview.3 je
výchozí základ, nikoli dokončení některého z nových checkpointů. Po realizaci
uvádět u konkrétní etapy commit, test evidence a případnou dosud otevřenou VM bránu.

- Každá etapa je samostatný funkční blok a commit; větší etapy mají níže podbloky.
  Přesný SHA/verzi zapisovat do release evidence až po skutečném ověření.
- Plánování oddělit od zápisu: zobrazit změny, zkontrolovat dostupnost/práva,
  pak aplikovat potvrzenou transakci. Existing-invalid není existing-absent.
- Bez --force bypassu pro CA/klíče/journal/ledger. Zálohy mají privátní práva,
  omezenou retenci a neobsahují hesla v argumentech/výstupech/logu.
- Idempotence znamená rozpoznat existující stav, ne automaticky znovu provést akci.
  Nezávislé změny uživatele nikdy nestagovat/nepřepisovat.
- Dokumentovat konfiguraci, CLI help, migraci i negativní případy v témže bloku.
- Runtime: cílené testy, celé go test ./..., go vet ./..., Linux race; wire/schema
  změny i compatibility/fuzz. Release: dvojí čistý build, SHA/manifest a .deb testy.
- VM zásahy nadále provádí uživatel podle runbooku; booted tests nezaměnit za Docker
  container parser/installation smoke. CI není primární iterativní testovací smyčka.

## P01 — Obecný agent a runtime-independent balíček

Změny: tools/release, deploy/systemd, deploy/debian, runbook a deployment tests.

- Základní hlcp-agent .deb nevyžaduje Docker/Podman/uidmap. Runtime helper závislosti
  nabídne setup pouze při výběru daného adaptéru. Bash je explicitní závislost
  pouze akce používající /bin/bash; instalace interpreteru vyžaduje souhlas.
- Obecná user unit hlcp-agent.service používá /usr/bin/hlcp-agent a zachová
  user config/state paths; systémová unit používá /etc/hlcp/agent a vlastní účet.
- User/system jsou alternativní lifecycle jednoho node/journalu, ne dva paralelní
  agenti. Rootless Podman vždy pod vlastníkem kontejnerů. Docker socket access
  není automaticky přidané a wizard vysvětlí jeho root-equivalentní význam.
- Existující systemd adaptér nyní volá systemctl pro systémové units; user service
  sama nedává právo je měnit. Explicitně dokumentovat cílový manager a potřebná
  lokální práva, nepřidávat široké sudoers pravidlo. User-manager target může být
  samostatný kompatibilní konfigurační doplněk s vlastními testy.
- Migrovat starý název Podman unit/enable symlinky a případné manual overrides
  explicitně s obnovitelnou zálohou. APT upgrade nesmí sám přepnout ani restartovat
  user service; starý profil zůstane podporovaný během migrace.
- Neinstalovat duplicitně /usr/bin/hlcp do balíčku agenta, kde by kolidoval s
  controller package. Samostatný CLI balíček lze později vyčlenit řízenou migrací.

Akceptace: fresh Debian bez runtime nainstaluje core bez Podmanu; existing Podman
upgrade zachová účet/config/resources/key/journal; jediná aktivní unit; parser,
install/remove/purge a obě architektury. Živý reboot generic user/system a Podman
bez přihlášení doplní operátor, ne deklarace z container testu.

Commit: `Decouple agent packaging from container runtimes`.

Stav P01 (2026-10-13): implementováno pro lokální ověření; core .deb bez runtime,
generic user/system units a zachovaná legacy Podman unit, no-resource mTLS/heartbeat
regrese, preinst odmítnutí ručního systémového unit/mask a migrační runbook.
Windows test/vet a Linux race prošly; Debian 13 disposable-container test ověřil
fresh install bez runtime, upgrade z preview.3 se zachováním legacy enable symlinku
i override, konfigurace/key/journal a remove/purge. Parser není booted service test.
Čistý reprodukovatelný build je release gate; P01 samotné zatím není publikovaný
checkpoint A. Živá migrace názvu unit/reboot user/system a Podman bez přihlášení
zůstává VM branou pro operátora. Nový setup wizard ani script adapter nejsou hotové.

## P02 — Script kontrakt a kompatibilní konfigurace

Změny: agent config, protocol tests, controller resolver, CLI help/examples.

- Nový adapter script, type task, capabilities resource.control + script.
  Typ je již wire identifikátor, ne nový frame; starý controller musí nepodporovaný
  task odmítnout čistě. Nový controller dál ovládá staré container/service resources.
- Zachovat actions jako seznam pro inventář. Nové commands mapuje každou povolenou
  akci na absolutní executable + pevné argv, absolutní working_directory a timeout.
  Klíče commands musí přesně odpovídat actions; žádné zadávané runtime parametry.
- Executable /bin/bash s argv [/absolute/script.sh] je normální lokální konfigurace.
  Žádné stringové command, bash -c z controlleru, PATH lookup executable, templating,
  cwd závislé na SSH session nebo důvěra v klientské resource/action mapování.
- Explicitní omezené prostředí procesu bez servisních/CLI secrets; bootstrap
  variables nejsou děděny do skriptů. Povolit potřebné lokální env jen konfigurací.
- Počáteční limity: nejvýše 32 akcí/resource, pevné argv do 32 položek a 8 KiB,
  timeout default 10 s/max 20 s (pod současným 30s command result budgetem).
  Delší úlohy vyžadují samostatný end-to-end timeout kontrakt, ne tichý lokální override.
- `-check-config` neprovádí skripty; doctor ověří existenci/interpreter/práva.
  Skripty nejsou automaticky důvěryhodné ani read-only podle názvu akce.

Akceptace: valid/invalid/duplicate YAML; custom diagnose/reload actions;
  unsupported capability/type; neznámá akce nikdy nespustí proces; staré konfigurace
  a wire testy beze změny; dry-run jen validuje a nespouští script.

Commit: `Define allow-listed local script resource configuration`.

Stav při dokončení P02 (2026-10-13, před P03): konfigurační kontrakt implementován; přesné mapování
actions/commands, lokální absolutní Linux cesty, bounded argv/environment a timeout,
strict YAML, task/script capability resolver a konzervativní read_only klasifikace.
Žádný nový wire frame ani runtime dependency. Podrobnosti a příklad jsou v
[script kontraktu](18-script-resource-contract.md). Script startup má dočasnou
fail-closed bránu před otevřením journalu; `-check-config` validuje bez exec/probes.
Windows test/vet a Linux race testy prošly, včetně skutečného check-config CLI
bez spuštění připraveného skriptu, wire round-trip, custom actions a dry-run bez
command frame/ledger mutation. P02 není hotový executor ani publikovaný release;
P03 odstraní startup bránu až s bounded execution a recovery testy.

## P03 — Bounded script execution a recovery

Změny: adapter runner/script, dispatcher, agent observations, e2e tests.

- Přímé exec executable/argv, společný limit zachyceného stdout/stderr 4 KiB,
  drain i po dosažení limitu, validní UTF-8 a explicitní indikace truncation.
- Exit 0 => success; nonzero => definitive failure. Timeout/killed/crash po
  zahájení může mít side effects: explicit unknown výsledek, ne safe-to-repeat.
- Linux process group cancellation s bounded TERM/KILL/wait, test potomků a
  zadržených pipe. Není to sandbox: skript může opustit process group; plná
  containment by potřebovala cgroup/scope a není slib první verze.
- Použít existující durable command journal/ACK/result/ID reconciliation.
  Stejné ID neprovádí další vedlejší účinek; změna payloadu je conflict. Nepřidávat
  automatic re-execution after restart ani tvrzení exactly-once pro externí svět.
- Script task nemá automatickou periodickou observations akci: stav unknown /
  NOT_OBSERVED, nikoliv error jen proto, že nemá kontejnerový stav. Ani pojmenování
  akce status ji nepřepne do polling loopu. Periodický probe je samostatné rozšíření.
- Heartbeat, ACK a read queries nezablokuje flood/hanging script. Output není
  automaticky bezpečný: nepropagovat secret-bearing env a nelogovat raw výstup do
  globálního auditu; výsledek je dostupný jen stávající autorizovanou result cestou.

Akceptace: reálný dočasný Bash script bez runtime; exit/output/flood/timeout,
  descendants/cancel/shutdown, injection-looking IDs, duplicate IDs, ztráta spojení,
  restart agenta/controlleru a žádný automatický replay. Docker/Podman regrese.

Commit: `Execute bounded configured scripts through the command lifecycle`.

Stav P03 (2026-10-13): implementován Linux script executor, přímé exec s pevnými
argv/cwd, čistým environmentem a lokálními overrides. Společný stdout/stderr
capture 4 KiB s UTF-8 a truncation markerem, process-group TERM/KILL a bounded
wait. Nereaped proces drží execution slot, aby nevznikala neomezená řada procesů.
Script status není read-only a tasks se nikdy nepollují. Použit stávající durable
journal bez změny wire verze nebo replay semantics; balíčky obsahují neaktivní
script příklad a provozní runbook. Windows test/vet a Linux race prošly; reálný
Bash testuje output/flood/nonzero/signal/timeout/descendants/held pipes/env/cwd,
mTLS e2e ověřuje heartbeat při floodu, ACK, ztracený výsledek, restart obou stran,
shutdown i tvrdý pád procesu agenta a žádný replay. Checkpoint A ještě potřebuje
čistý reproducible build/package release gate a operátorskou VM akceptaci;
neprohlašovat container/parser testy za živý reboot nebo production containment.

Release gate checkpointu A (2026-10-13): dokončen; dva čisté buildy a všech 16
artefaktů shodné, lokální i remote SHA256 ověřeny, Debian lifecycle/legacy upgrade
testy prošly. Publikovaný privátní preview.4 míří na
`2dd68dd3a4c87d4dc1ec54c9175e137e6879ec3c`.
[Přesná release evidence a otevřené VM brány](19-checkpoint-a-release-evidence.md).
Operátorská VM akceptace není tímto zápisem uzavřena; následuje implementace P04.

## P04 — Sdílený non-destructive setup engine a doctor

Změny: nový interní setup balík, hlcp/agent/controller command entrypoints.

- Read-only doctor: OS/arch/time sync, paths/owners/modes, unit overrides, account,
  runtime availability, subordinate UID/GID, user manager/linger, network/TLS expiry.
- Setup engine vytváří plán změn a aplikaci se zálohou/rollbackem; file FD/no-clobber,
  symlink/unsafe parent checks, bounded input a transakční zápis. Nepřepisuje ruční
  konfiguraci jen proto, že neodpovídá template. Opakované spuštění je bezpečné.
- Průvodce i noninteractive explicitní options používají stejné validace. Tajemství
  hidden prompt/stdin/private files; nikdy argv/env/log. Bez TTY nečekat na prompt.
- Role/install account není odhadnutý z neověřeného env bez kontroly; root zapisuje
  user CLI konfiguraci pouze pro explicitně zvolený validovaný lokální účet.
- Privilegované změny účtu/linger/unit/firewall se zobrazí a vyžadují souhlas.
  Firewall nikdy neotevírat automaticky široce; zobrazit konkrétní síťový požadavek.

Akceptace: žádné writes pro doctor/preview; mocked OS effects; existing-invalid,
  permissions/symlinks, interruption/disk-full/partial write rollback; secret-free
  output; help/version dál offline. Go test/vet/race pro sdílený engine.

Commit: `Add validated setup planning and deployment diagnostics`.

P04 je rozdělena na menší ověřitelné bloky: P04a read-only plánovací jádro,
P04b transactional Apply/backup/rollback/recovery a P04c read-only doctor/CLI.
P04a zavádí interní kontrakt plánů, bounded vstupy, validaci existing i proposed
obsahu, explicitní replacement selection, stale recheck a Linux descriptor-relative
source s NOFOLLOW/owner/mode kontrolami. Žádný Apply ani uživatelský instalační
příkaz zatím neexistuje; celé P04 tím není dokončené. Pravidla a zbývající brány
shrnuje [P04a dokumentace](20-setup-planning-core.md).

Stav P04a (2026-10-13): implementováno a lokálně ověřeno. Windows test/vet,
Linux race/vet, bounded fuzz (poslední kampaň 75 921 běhů) prošly. Linux arm64
compile-only gate prošel, nikoli živý ARM test. Preview a recheck nevytvářejí
konfiguraci, secrets, lock files ani backupy; unsupported native OS fail closed.
P04b/P04c zůstávají otevřené, nový preview se v tomto bloku nevydává.

P04b: interní potvrzené transakce v jednom privátním existujícím adresáři,
descriptor-relative exclusive staging, Linux NOREPLACE/EXCHANGE, fsync bariéry,
strict bounded manifest, rollback a zotavení podle durable fáze. Záloha je jediná
a její odstranění vyžaduje samostatné potvrzení; nový mutující plán ji sám neeviktuje.
Konflikty a neznámé/corrupt/orphan state zůstávají zachované pro inspekci, nikoli
force overwrite. Detaily, hranice a otevřené P04c/P05 brány jsou v
[transaction dokumentaci](21-setup-transactions.md). Nejde o setup CLI ani VM zásah.

Stav P04b: implementováno a lokálně ověřeno. Windows test/vet a Linux race/vet
prošly, transakční testy prošly třikrát opakovaně a metadata fuzz prošel
233 871 běhů. Linux arm64 compile-only prošel. Native testy pokrývají i racing
editor, explicitní účet, pády po exchange/commit markeru a během cleanup.
P04c doctor/entrypoints a P05 wizard integrace zůstávají otevřené; preview.4 se nemění.

P04c první blok: offline doctor entrypoints v hlcp/agent/controller, bounded
read-only konfigurace/TLS soubory, key match/expiry/identity a configured executable
lookup. Žádný API token, journal, ledger, execution ani síťové probe.
Kompletní host/systemd/rootless readiness a opt-in peer probe zůstávají otevřené;
P04 není uzavřené. Kontrakt a hranice: [offline doctor](22-offline-doctor.md).

Navazující P04c blok přidává opt-in --host-checks s kernel clock, ověřeným
effective account, systemd properties/overrides, user manager/linger a Podman
subordinate ranges. Jen pevné read queries s timeout/output limity, žádné
impersonation, aktivace, mapping-helper nebo container probe.
Zbývají script/cwd readiness, explicit root-to-user inspekce, transaction inspect
a peer TLS probe; P04/P05 a nový release tedy nadále nejsou uzavřené.

Další P04c blok přidává statické Linux script executable/cwd/shebang kontroly
(bez execution; env interpreter a loader/argv dependencies nejsou potvrzené)
a explicitní --setup-dir napojený jen na read-only Planner.Inspect.
Pending/corrupt state se neobnovuje ani nemaže. P04 nadále čeká na explicitní
root-to-user inspekci a opt-in peer TLS probe; skutečná VM akceptace a P05
wizard integrace zůstávají otevřené.

P04c TLS blok: výslovný agent --probe používá již validované PEM snapshoty a
sdílenou runtime TLS politiku (TLS1.3, CA/name/explicit EKU/hlcp ALPN), bounded
3s dial/handshake bez HELLO/registrace. Rozlišuje server identity success od
neověřeného client admission/allowlist; síťové audit logs jsou možný side effect.
Zbývá explicit root-to-user inspekce; P04/P05 ani nový release nejsou uzavřené.

## P05 — Imported TLS setup jako první použitelný průvodce

- Controller setup importuje server cert/key + agent trust bundle, ověří key match,
  validity/chain/basicConstraints/EKU/SAN pro zvolenou IP/DNS a připraví env/policy/
  tokeny/allowlist/CLI. Leaf ani privátní CA klíč neposílá do user CLI konfigurace.
- Agent setup importuje client cert/key + controller trust bundle, exact node URI
  a EKU; podporovat oddělené trust bundles pro externí server/client PKI.
- Ponechat možnost použít již existující soubory/migraci; import cizího leafu
  neimplikuje ovládání externí CA. Obnova externím nástrojem, HLCP expiry diagnostics.
- Teprve po validaci a potvrzení start/enable. Agent nezačne ovládat resources
  během importu. Node admission explicitní lokální operator operace.
- Externí obnovy mohou bezpečně stage nové soubory a vyvolat řízené načtení;
  dokumentovat snapshot LoadCredential a současné restart-only TLS chování.

Akceptace: první fresh booted controller + script-only agent s externí PKI bez
  OpenSSL příkazů v instalačním postupu; missing/wrong/expired cert fail closed,
  root CA private key není vyžadovaný, resources [] a user CLI funkční.

Commity: `Add imported-certificate controller setup`; `Add imported-certificate agent setup`.

## P06 — Managed PKI a controller setup

- Vestavěný Go issuer v controlleru; bez povinného externího CA serveru/Vault.
  Root EC P-256 s pathLen 1 a encrypted export, issuing CA pathLen 0, neagentní
  podpisový klíč dostupný jen kontrolované issuer části. Žádné agentní CA certs.
- Proposed defaults: root 10 let, issuer 1 rok, leaf 90 dní, obnova po 30 dnech;
  nikdy leaf platnost za issuer/root expirací. Limity konfigurovat centrálně a testovat
  s fake clock, nejsou to předem vydané produkční certifikáty.
- Root podepíše issuer a první serverAuth leaf; server SAN z explicitní adresy/DNS.
  Managed setup vytvoří a ověří privátní zálohu rootu; off-host uložení potvrdí
  člověk. Pracovní root key odstranit jen samostatným souhlasem po ověření zálohy.
- Unattended issuer key se chrání OS/account/LoadCredential; encrypted disk backup
  ano, heslo vedle klíče nepředstírá ochranu před root kompromitací. Provozní
  issuer key je nutně online. Root key není credential běžícího controlleru.
- Controller setup naváže P04/P05 a připraví normální CLI; záloha/restore pokryje
  issuer key, trust, node registry, policy a provozní ledger zvlášť od root exportu.
- Evidovat serialy/validitu/issuer pro diagnostiku bez tajemství. Dokumentovat issuer
  expiry: obnova vyžaduje offline root; varovat výrazně před expirací.

Akceptace: správný chain/EKU/SAN/pathlen, encrypted root export/restore, server key
  match, opakovaný setup nemění CA, žádný root key v runtime credentials, reboot
  bez zadání root hesla. Vydání odmítne nesprávné CSR, CA požadavky a špatné limity.

Commity: `Add managed PKI generation and protected issuer storage`;
`Integrate managed controller setup and private backup workflow`.

## P07 — Durable node registry a operátorská správa admission

- Versionovaný bounded durable registry pod /var/lib/hlcp-controller, oddělený
  od command ledgeru. Transakční update se serializací a crash-consistent zápisem;
  nezavádět samostatnou DB službu. Registry obsahuje node identity/generation,
  admission status a bounded enrollment výsledky. Události mají retenční limit.
- Explicitně importovat stávající nodes.json; neudržovat dva nezávislé zdroje pravdy.
  Legacy startup-only allowlist zůstává v kompatibilním režimu; přepnutí zvolí setup.
- Operator-only local API pro create/remove/re-pair; observer/MCP read-only nemůže
  vytvářet pozvánky. Správa běží přes loopback 9444, bez nové vrstvy uživatelských rolí.
- Live revoke zastaví renewal, odmítne nová spojení a zavře aktivní session node.
  Zohlednit už přijaté operace: nevracet side effects zpět, zachovat unknown výsledky.
- Re-pair kompromitované stejné identity musí odmítnout staré credentials pomocí
  generation/registrovaných keys/serialů, ne jen znovu přidat stejný node ID.

Akceptace: concurrent admission/remove/reconnect/renew; crash/restore;
  old allowlist migration, duplicate ID a active-session revocation, forbidden
  observer writes, žádná ztráta operation ledgeru nebo auto replay.

Commit: `Add durable live node admission and revocation`.

## P08 — Párovací pozvánky a izolovaný enrollment vstup

- Pozvánka: version, controller HTTPS enrollment URL, SHA-256 root DER fingerprint,
  node ID, >=128bit random secret, default expiry 10 minut. Žádné root keys ani
  dlouhodobý operator token. Skrytý vstup/přenosný privátní soubor, ne argv.
- `hlcp pairing create --node ID`, list bez secrets a revoke. Registry uchovává
  digest secretu a stav, nikoli plaintext. Bez reusable/all-node pozvánek v MVP.
- Oddělený HTTPS enrollment listener (navržený privátní port 9445, explicitní bind),
  minimální bootstrap/issue endpointy; žádné přesměrování admin API 9444 do sítě.
  Runtime HLCP 9443 dál vyžaduje mTLS. LAN/WireGuard dostupnost je podmínka, ne VPN služba.
- Bootstrap poskytne pouze bounded veřejný CA chain. Agent ho ověří proti otisku
  přenesenému z důvěryhodného controller CLI, potom provede standardní TLS
  chain/hostname validation; pairing secret nikdy neposílá před tímto ověřením.
- CSR podpis + public key ownership; server sám sestaví node identity/EKU/SAN,
  odmítne CA fields/jinou identitu. Časové limity/body limits/rate limiting/clock
  checks, audit bez tokenu a bez důvěry v údaj o adrese z proxy headers.
- Durable atomic consume + node admission + recovery-issued result. Retry stejného
  enrollment request ID/CSR vrátí tentýž výsledek během bounded retry okna;
  stejný token/jiné key nebo identity je replay a selže. Crash nesmí token znovu
  odemknout ani nechat trvale admitovaný node bez získatelného certifikátu.

Akceptace: fingerprint mismatch/MITM/hostname mismatch nikdy neuvidí secret;
  expired/revoked/wrong-node/replay/concurrent token; lost response a restart
  controlleru; bounded storage cleanup; public admin access neexistuje.

Commity: `Add durable single-use node pairing invitations`;
`Add pinned HTTPS certificate enrollment with retry recovery`.

## P09 — Agent join a resource wizard

- Join jako skutečný uživatel: ověřit prostředí, lokálně vytvořit key/CSR, použít
  P08, ověřit issued chain/key/identity, atomicky uložit cert/config, resources [].
- Vybrat user/system lifecycle a adaptéry; případná instalace runtime je potvrzená
  privileged operace. Zobrazit práva, nastavit user manager/linger jen se souhlasem.
- Idempotent retry nedělá nový node/key, pokud už vzniklo durable enrollment;
  existing config/key/journal chráněné proti overwrite. Resumable incomplete setup.
- Resource wizard lokálně nabídne kontejnery vlastního účtu a explicitní systemd
  units/scripts. Discovery nic nepovoluje; default mutace nejsou preselected.
- U script resource zadat ID/názvy akcí, executable/pevné argv/cwd/timeout;
  nevykonávat neznámý script jako součást validace. Žádné vzdálené uploady skriptů.
- Po potvrzení validovat config, bezpečně restartovat správnou unit, čekat bounded
  na registration/inventory a nabídnout diagnostiku. Neprovádět restart resource
  jako automatický instalační smoke; opt-in status pouze pro známý adapter.

Akceptace: instalace script-only node bez runtime; Docker/rootless Podman/mixed
  node, user/system lifecycle, linger reboot, no-TTY errors, wrong permissions,
  pairing interruption/retry; connection OK != runtime action OK.

Commity: `Add resumable agent pairing and service setup`;
`Add explicit local resource configuration wizard`.

## P10 — Cert lifecycle, warnings a bezpečné načítání

- Agent renewal přes HTTPS s platným mTLS certifikátem, registry identity/generation
  kontrola, vydání jen pro stejnou identity/key policy. Pairing token již není potřeba.
- Periodic renewal předem s jitter/backoff; při nedostupnosti používá platný leaf.
  Expired node re-pair, nikdy insecure grace period. UI/CLI ukáže poslední úspěch,
  next attempt a expires-in bez privátního obsahu/cest. Imported mode nenabízí issuer.
- Atomické stage/validate/swap cert-chain/key a hot-load pro NOVÉ TLS handshakes/
  reconnect; zachovat poslední validní snapshot při chybě. Nezakládat obnovu jen na
  změně zdrojového souboru, který systemd LoadCredential službě automaticky neobnoví.
  Explicitní privátní writable managed-cert storage s ownership/hardening review.
- Obnovovat i server leaf; bezpečně zkoordinovat TLS snapshot a enrollment endpoint.
  Issuer/root expiry je odlišný lifecycle s jasnými včasnými varováními.
- Default expiry warnings 30/14/7/1 dní; při jiných platnostech clamp relevantní
  thresholds. CLI doctor, journal a budoucí API veřejná metadata. Aktivní notifikace
  mimo aplikaci je opt-in integrace, ne tvrzení, že journal někoho upozornil.
- Revocation pro renewal i runtime; obnova není záruka neexpirace. Existing session
  policy při expiraci určit a otestovat (cílově zavřít a odmítnout nový handshake).

Akceptace: fake clock hraniční časy/clock skew, offline/backoff/recovery,
  expired issuer/leaf, generation revocation, disk-full/partial-write, old snapshot,
  concurrent renew/handshake a server reboot; žádná potřeba root hesla pro leaf renew.

Commity: `Add managed certificate renewal and atomic TLS reload`;
`Expose certificate lifecycle diagnostics and expiry warnings`.

## P11 — Ověřené stažení a update helper

- První jednoduchá cesta zůstává přímý .deb + Quick Start. Helper nabízí roli,
  verzi/kanál a OS/arch, stahuje do nového staging directory a ověří metadata.
- SHA256SUMS řeší integritu, ne původ: podepsaný release manifest, trust key
  připnutý v helperu a dokumentovaný key rotation/bootstrap. Žádné curl-to-root
  spuštění neověřeného shellu. Privátní cesta využívá vlastní gh/keyring login;
  veřejná distribuce později bez loginu, nikoli automatická změna visibility repo.
- Explicitní update, žádné background auto-upgrades/default restart. Zobrazit
  verzi/kompatibilitu, backup state/config, instalovat přes APT a po souhlasu
  restartovat správný system/user service, ověřit registration/inventory.
- Dodržet state-reader/wire kompatibilitu pro rollback; neobnovovat starý journal
  jako způsob návratu binárky. Migrace user unit a původních binárek obnovitelná.
- Podepsaný APT repozitář je následná distribuční etapa, nikoli podmínka MVP.

Akceptace: modified/unsigned/wrong-arch/missing/incomplete assets, auth denied,
  downgrade, failed install/start, modified conffiles, package/state preservation,
  secret-free download logs a idempotent retry. Dva čisté shodné builds a registry
  SHA/digest verification před privátním preview release.

Commity: `Sign release manifests and verify installation artifacts`;
`Add explicit verified installation and update workflow`.

## P12 — Migrace aktuálního homelabu a release acceptance

- Nejprve read-only evidence skutečných VM: installed versions, active unit paths,
  key/cert match/expiry/basicConstraints, root backup, config owner/paths a state
  schemas. Necommitovat jejich secrets ani odhadovat fakta z template.
- Zapsat dnešní Podman resources a IDs; zachovat journal i ledger byte/history
  sémantiku. Nové registry/enrollment state mají vlastní konzistentní zálohu.
- Importovat současnou instalaci jako legacy/imported, pak explicitně migrovat na
  managed PKI. Pathlen:0 root => nový root; trust overlap obou autorit, stage nové
  leaves, ověřit registry/mTLS/CLI a teprve poté odstranit starou trust. Žádný tichý reset.
- Bezpečná údržba v pořadí controller kompatibilní verze, node registry, agent
  package/setup, cert migrace/renewal; po každém kroku recovery point a evidence.
- Testovací matice: fresh imported/managed controller; script-only; Docker;
  rootless Podman; mixed; user/system služby; offline node; wrong/revoked cert.
  Arm64 archive/build smoke není živá arm64 akceptace. Docker CLI socket privilege
  a rootful profily uvést explicitně podle skutečného otestovaného scope.
- Booted VM bez loginu, revoke aktivní session, cert renew/expiry/re-pair, controller
  restore, upgrade/compatible rollback, output flood vs heartbeat, same-ID no replay.
- Quick Start na novém Debianu podle README od stažení po první opt-in akci;
  žádný ruční YAML/OpenSSL/scp v managed cestě. Změřit kroky/time/memory/disk,
  neprohlásit lightweight nebo plug-and-play pouze podle unit tests.

Commity: `Document and verify legacy-to-managed deployment migration`;
`Publish tested plug-and-play quick start and acceptance evidence`.

## Pořadí a release checkpointy

| Checkpoint | Etapy | Výsledek pro uživatele |
|---|---|---|
| A | P01–P03 | Obecný agent bez runtime + lokální allow-listed script akce |
| B | P04–P05 | Doctor a průvodci s vlastními certifikáty, bez ručního YAML |
| C | P06–P09 | Managed setup + bezpečné jednorázové join + výběr resources |
| D | P10 | Obnova, TLS reload, expiry diagnostics a revoke coverage |
| E | P11–P12 | Ověřená distribuce/update, migrace a skutečný Quick Start |

P11 release podpisy lze připravovat nezávisle po P04; update/service orchestrace
potřebuje stabilní profily P01/P09. Managed checkpoint C není konečný stabilní
release bez lifecycle D. Každý checkpoint publikovat jako preview až po lokální
akceptaci a ověření artifactů; verze určit v době vydání, neměnit existující tagy.

## Definition of done celého plánu

- Nový uživatel nainstaluje a spáruje funkční agent bez OpenSSL/YAML/scp kroků.
- Stejně přímočaře může použít svou PKI bez předání CA privátního klíče.
- Script-only instalace netahá Docker/Podman a nepovolí žádné vzdálené shell strings.
- Discovery a provisioning nepřidávají implicitní resource actions/privileges.
- Managed leaf renewal funguje po rebootu, expiry/revoke jsou viditelné a fail closed.
- Repeated setup/update/retry/crash zachovávají identity, secrets a operation history.
- Stávající Podman homelab přejde bez ztráty dat; ostatní runtime/OS evidence je
  přesně pojmenovaná, chybějící live test je otevřená brána, ne dokončená funkce.
- Dokumentace jasně rozlišuje implemented/released/proposed a nenutí další ACL
  profily pro běžného jediného vlastníka homelabu.
