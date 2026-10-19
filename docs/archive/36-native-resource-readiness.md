> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05n — Native Linux resource metadata preflight

Stav: zdrojová implementace, součást native user-scope `hlcp-agent start`.
Není v publikovaném preview.4. P05/checkpoint B ani live package/VM acceptance
se tímto neuzavírají. Žádné nové konfigurační přepínače ani permission profily.

## Co se ověřuje

Kontroly běží přímo jako registrovaný non-root resource owner. Root neprovádí
inspekci user resources ani impersonaci. Prázdné resources nepotřebují runtime;
samotné capabilities nebo compatibility název Podman jednotky ho nevynucují.

| Adapter | Read-only kontrola | Omezení této etapy |
|---|---|---|
| Docker | Root-owned ELF `/usr/bin/docker`, root-owned socket `/run/docker.sock`, owner/group POSIX read/write access | Standardní lokální rootful daemon; žádný socket connect, daemon API či group grant |
| Podman | Root-owned ELF `/usr/bin/podman`, validní rozsahy `/etc/subuid` a `/etc/subgid`, root-owned setuid ELF `newuidmap`/`newgidmap` | Rootless Debian profile; capability-only helpers, jiná cesta a rootful profily nejsou touto kontrolou pokryté |
| Script | Executable a pracovní adresář, fixed absolute interpreter u shebangu, bounded content fingerprints | Bez execution; `/usr/bin/env` indirection a neznámý formát se odmítají; interpreter musí být důvěryhodný root-owned ELF |
| systemd | Root-owned `/usr/bin/systemctl` | Jen status; mutace čekají na skutečné service authorization ověření |

System-scope s neprázdnými resources nadále vrací RESOURCE_PREFLIGHT_REQUIRED
před resource inspekcí. Účet `hlcp`, jeho sandbox a skutečná oprávnění musí mít
samostatnou akceptaci; root preflight není náhrada.

## Souborový a lifecycle kontrakt

Soubory se čtou pouze read-only s no-follow/nonblocking/CLOEXEC otevřením,
limity, regular/single-link a ownership/mode kontrolou. Neznámé owners,
group/world-writable executable/parent a privátní script setuid se odmítají.
Root-owned sticky `/tmp` je dovolen pro diagnostické fixtures. Interpreter a
distro executable mohou použít standardní usr-merge symlinky; výsledná cesta
musí splnit trusted file/ancestor pravidla. Journal se neotevírá ani nezamyká.

Resource executable limit je 8 MiB, distro executable/interpreter 64 MiB,
subordinate mapping file 1 MiB. Core omezuje počet resources a script kontrol.
Kontroluje se obsah, identity, mode, owner a změny během čtení; neuchovávají se
celé binárky ve výsledném activation proof. Atime a directory mtime nejsou
důkazem, aby čtení či běžná práce v adresáři nezneplatnily plán.

Resource fingerprint je součástí source activation snapshotu. Kontrola se opakuje
před i po lifecycle změnách, stejně jako config/TLS/service preflight. Změna
závislostí před startem odmítne stale plan; změna po provedeném kroku znamená
nejistý výsledek, ne automatický retry/rollback. Preview nevypisuje privátní paths,
argv, environment nebo fingerprints; controlled refusal nezveřejní OS chybu.

## Co stále není garantováno

Jde o metadata, nikoli readiness/health probe daemonu nebo zkušební execution.
Neověřuje se existence cílového kontejneru, cgroup/storage, script dependencies,
loader/architecture/noexec, ACL/MAC ani prostředí a supplementary groups již
běžícího user manageru. Runtime dědí prostředí služby; její PATH/DOCKER_HOST
může být jiný než standardní dependency paths z této kontroly. Neověřené oblasti
zůstávají explicitně uvedené v core preview a CLI help. Úspěch preflight není
slibem úspěchu akce ani atomickou ochranou vůči změně po poslední inspekci.

## Testovací evidence a navazující práce

Native testy pod skutečným non-root UID/GID v dočasném Linux kontejneru ověřují:
script read-only marker, content/directory proof changes, stale-plan refusal před
startem, hardlink/write/setuid/format/indirect interpreter refusal, cancellation,
nejistý výsledek při změně souboru po dispatch startu bez automatického opakování,
POSIX owner/group/other precedence a root refusal. Samostatný explicitní root
fixture pouze v dočasném kontejneru vytvoří syntetický socket a ELF helper files,
pak pod non-root ownerem testuje accepted fingerprints a odmítnutí world-writable
socketu, nedostatečných socket práv a chybějícího setuid helperu. Nepřipojuje se
na socket ani nespouští syntetické runtime programy; není to Docker/Podman health test.

Navazuje actual service-sandbox/system resource readiness, fresh package setup
compatibility, legacy migration a booted Debian user/system service/reboot
acceptance. Distribuce nové verze je samostatný checkpoint, ne součást této změny.

Lokální validace: Windows full test/vet, Linux full race/test/vet bez sítě,
explicitní disposable runtime fixtures a native testy pod non-root UID/GID,
Linux ARM64 compile-only. Minimální build container nemusí obsahovat systemd;
kontrola chybějícího executable není live service test. Tyto výsledky nedokazují
VM install/start/reboot ani skutečnou Docker/Podman lifecycle akceptaci.
