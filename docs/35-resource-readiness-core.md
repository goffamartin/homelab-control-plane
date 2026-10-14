# P05m — Resource readiness: společná pravidla

Stav původního P05m: implementovaný a testovaný interní core bez native hostu.
Navazující [P05n](36-native-resource-readiness.md) již přidává Linux host a user-scope
napojení do `hlcp-agent activate`. Tento dokument popisuje samostatný core;
preview.4 se nemění. System-scope resources mají nadále samostatnou otevřenou bránu.

## Co tento blok připravuje

`PlanResourceReadiness` přijímá validní agent config, pevný service profil a
read-only host interface. Neprovádí akce, nespouští runtime CLI, neotevírá journal,
nepřidává skupiny ani oprávnění. Host může pouze inspektovat závislosti.

- Docker: executable a přístup k lokálnímu socketu.
- Rootless Podman: executable a subordinate UID/GID mappings včetně helper readiness.
- Scripts: executable, pracovní adresář a případný interpreter pro každou pevně
  nakonfigurovanou akci; žádné spuštění skriptu ani automatické status polling.
- User-scope systemd: pouze status; mutace nelze povolit podle práv CLI uživatele
  bez ověření skutečné autorizační cesty služby.

Kontroly mají pevné pořadí; script resources a actions se řadí deterministicky.
Maximum je 256 resources a 128 script kontrol. Nevalidní config, nepodporovaný
profil, chybějící host, selhání nebo prázdný fingerprint se odmítají. Prázdný seznam
resources nepotřebuje runtime ani host. System-scope s resources se odmítá před
inspekcí: práva roota nejsou důkazem připravenosti sandboxu účtu `hlcp`.

Výsledek obsahuje pouze statické kódy kontrol a neověřené oblasti. Veřejné JSON,
String a GoString neobsahují cesty, argv, environment ani interní fingerprint.
Interní důkaz váže profil, přesný config a fingerprints hostu; změna konfigurace
mění důkaz. Návratová chyba je rozhodující: částečný preview není úspěšná kontrola.

## Co kontrola nezaručuje

Metadata nejsou důkazem existence kontejneru, zdraví daemonu, správného PATH či
DOCKER_HOST prostředí služby, funkčnosti loaderu, mount/ACL/MAC oprávnění ani
úspěchu následné akce. Skript pojmenovaný status může měnit stav. Tyto oblasti
zůstávají explicitně neověřené; core není oprávnění ke spuštění služby.

## Následující blok a acceptance

Navazující Linux host musí ověřovat skutečného non-root uživatele, bezpečně a omezeně číst
soubory, odmítat nebezpečné ownership/permissions a změny během čtení. Bez socket
connect, runtime příkazů nebo resource execution. Poté se jeho důkaz připojí do
activation snapshotu a znovu ověří před i po lifecycle změně. Teprve tento blok
může rozšířit současnou prázdnou activation gate.

Core testy pokrývají mixed profily, pořadí, redakci výstupu, obranné kopie preview,
config-bound proof, odmítnutí system scope/mutující systemd před hostem, bounded
checks, chyby hostu, nulové důkazy a cancellation. Native POSIX, skutečný systemd,
VM reboot a fresh package acceptance tím nejsou otestované. P05/checkpoint B
zůstává otevřený; žádný nový release ani VM změna není součástí této etapy.

Lokální evidence: Windows full test/vet, Linux full race/test/vet v izolovaném
kontejneru bez sítě a Linux ARM64 compile-only. Script-only compatibility profil
má vlastní regresní test: ani název Podman jednotky nevyvolá container kontrolu,
pokud v konfiguraci nejsou container resources.
