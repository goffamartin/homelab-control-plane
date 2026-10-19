> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05e — Imported controller setup CLI

Implementováno ve zdrojovém kódu od 2026-10-14, nikoli v publikovaném preview.4.
Připravuje konfiguraci s vlastními certifikáty; neinstaluje balíčky, nevytváří
service account, nemění firewall, nespouští služby ani nekonfiguruje uživatelské CLI.
P05 a release checkpoint B proto zatím nejsou dokončené.

## Použití

Na Linuxu jako root, s vlastními PEM vstupy v bezpečných absolutních cestách:

```sh
sudo hlcp-controller setup \
  --listen 192.168.0.15:9443 --server-name 192.168.0.15 \
  --cert /root/import/controller-chain.pem \
  --key /root/import/controller.key \
  --peer-ca /root/import/agent-trust.pem \
  --issuer-ca /root/import/controller-issuer-trust.pem \
  --admit debian-01
```

Výchozí režim pouze ukáže plán. Stejný příkaz s `--apply --yes` výslovně
potvrdí zápis. `--json` poskytne report bez obsahu souborů a tajemství.
`--interactive` na skutečném terminálu doplní chybějící metadata/cesty a vyžádá
napsání `apply`; nelze jej kombinovat s `--apply`, `--yes` ani `--json`.
`setup --help` nečte soubory a nevyžaduje root.

Výchozí cíl je `/etc/hlcp/controller`; `--directory` dovolí jiný privátní cíl,
ale neupraví pevné cesty distribuované systemd unit. Bezpečný rodič cíle musí
existovat, chybějící finální adresář vznikne až po potvrzení. Nejde o rekurzivní
bootstrap `/etc/hlcp`. Čtení vstupů je omezené velikostí a kontroluje vlastnictví,
typ souboru a privátní oprávnění klíče. CA privátní klíč se nepředává.

Server chain se ověřuje proti `--issuer-ca`, agent trust pochází z `--peer-ca`;
mohou to být různé autority. Ověřuje se server-auth, SAN jméno/IP, platnost,
key match a řetězec. Bind vyžaduje konkrétní IP a port 1024–65535, nikoli wildcard.
Setup neověřuje dostupnost listeneru ani síť. Lokální API zůstává podle standardní
unit na loopbacku 127.0.0.1:9444.

## Tokeny, opakování a admission

- Náhled ani zrušení nečtou generátor náhody a nevytvářejí bearer tokeny.
  Interní neprivátní stand-ins pouze ověřují tvar plánu; jejich plán nelze aplikovat.
- Po potvrzení se nejprve znovu ověří platnost TLS a původní snapshot. Teprve
  potom vzniknou dva nezávislé kryptograficky náhodné tokeny po 32 bajtech,
  uložené jako 64 hex znaků do `api.token` a `observer.token`.
- Existující platná dvojice se zachová; opakování negeneruje náhradu ani nerotuje
  přístupy. Chybí-li jen jeden token, setup odmítne automatickou opravu.
- `--admit` lze opakovat pro explicitní node identity. Čerstvá instalace bez něj
  má prázdný seznam, tedy deny-all. Opakování bez něj zachová existující admission.
  Explicitní jiný seznam je konflikt, ne příkaz k jeho přepsání.

Devět souborů a baseline operator/observer policy odpovídají
[P05d core](26-imported-controller-setup-core.md). Všechny mají mode 0600.
Existující odlišná konfigurace, necanonical obsah nebo vlastní policy mohou vést
ke konfliktu: toto není automatická migrace ručně upravené instalace.
Žádný force, reset, automatická recovery, rotace ani discard záloh.

Finální plán s reálnými tokeny používá původně připnuté adresářové deskriptory
a musí mít stejné změny jako odsouhlasený náhled. Nově vzniklý cíl mezi náhledem
a potvrzením se nepřevezme. Selhání generátoru nezapisuje soubory ani adresář.
Samotný zápis používá P04 transakci a P05c tvorbu adresáře; vznik adresáře a celý
file batch nejsou jedna globální atomická operace. Historie ledgeru/journalu se
neotevírá ani nemění. Signál během apply využívá transakční cancellation.

Exit codes: 0 náhled/aplikace/zrušení, 1 bezpečnostní nebo transakční odmítnutí,
2 neplatné přepínače nebo chyba výstupu. Chyba během apply není důkaz, že nic
nevzniklo; zachovat stav pro inspekci. Také chyba výstupu po zápisu neruší commit
souborové transakce. Diagnostika neobsahuje PEM klíče ani bearer hodnoty.

## Ověření a otevřené brány

Windows full test/vet a Linux full race/test/vet prošly; následný native Linux
CLI test a cílený race/vet ověřují skutečné fresh/repeat použití bez změny tokenů
a admission. Testy zahrnují preview/cancel bez RNG a writes, partial token pair,
selhání RNG, raced target, privátní modes, redakci výstupů a offline help.
Arm64 ověření je compile-only, nikoli živá VM acceptance.

Navazuje service/account provisioning, samostatné potvrzení user CLI přístupu,
explicitní start/enable a reboot/TTY acceptance na VM. Certifikáty této imported
cesty obnovuje vlastní PKI; managed issuer a automatická obnova jsou pozdější fáze.
Existující release ani konfigurace běžícího homelabu se tímto blokem nemění.
