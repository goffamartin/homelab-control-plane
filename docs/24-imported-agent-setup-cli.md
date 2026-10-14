# P05b — Imported agent setup CLI

Ve zdrojích od 2026-10-14, nikoli v preview.4. Tento krok připraví konfiguraci
čerstvého generic agenta z dodané PKI; neinstaluje balíček ani neaktivuje službu.
Pro dnešní existující Podman konfiguraci nejde o migration/force update.

## Parametrická cesta

Spouštějte přímo pod runtime účtem; user scope nepoužívá sudo. V následujícím
příkladu musí cílový adresář už existovat, být vlastněný aktuálním registrovaným
UID/primary GID a mít mode 0700. PEM input paths jsou absolutní, bezpečné
regular single-link soubory bez symlinků/unsafe parentů; key má private mode.
Directory provisioning zatím není implementováno, neprovádí se implicitní mkdir.

```sh
hlcp-agent setup \
  --scope user \
  --node debian-01 \
  --controller 192.168.0.15:9443 \
  --server-name 192.168.0.15 \
  --directory /home/martin/.config/hlcp \
  --journal /home/martin/.local/state/hlcp/command-journal.json \
  --cert /home/martin/import/client-chain.pem \
  --key /home/martin/import/client.key \
  --peer-ca /home/martin/import/controller-trust.pem \
  --issuer-ca /home/martin/import/client-issuer-trust.pem
```

Default je pouze náhled pěti souborů. Po jeho kontrole stejné options doplňte
o --apply --yes. --json nabízí strojový report preview nebo applied.
--apply bez --yes a samotné --yes jsou usage error, nikoli implicitní consent.
System scope vyžaduje skutečné root UID a explicitní --scope system.
Root se --scope user odmítne; account není odhadnutý z HOME/SUDO_USER.
Setup zatím nepodporuje root --user run-as z doctoru, aby nezapisoval pod
odhadnutým uživatelem. Všechny paths jsou explicitní, žádná home auto-discovery.

## Interaktivní cesta

```sh
hlcp-agent setup --interactive
hlcp-agent setup --help
```

--interactive vyžaduje skutečný Linux TTY (TCGETS), ne pipe, obyčejný reader
nebo /dev/null. Bez TTY selže před čtením inputu. Help je offline a nevyžaduje
konfiguraci/certifikáty/token. Wizard se ptá jen na chybějící metadata a cesty
(zadané flags ponechá), nikoli na PEM, passwords nebo CA private key.
Po validaci zobrazí shodný transakční plán jako parametrická cesta a požaduje
doslovné apply. Jiná odpověď znamená cancellation bez zápisu; EOF/oversized
odpověď je fail bez zápisu. Každá odpověď má bounded 4096 bytes.
Interactive se nekombinuje s --apply/--yes/--json.

## Bezpečnost a opakování

- P04 bounded reader pro každý input (1 MiB), key bez group/other read,
  žádný shell/network/runtime probe. PKI validace má P05a oddělené issuer/peer trust.
- Journal musí být mimo config directory; CLI jej nečte, neotvírá ani nevytváří.
- Resources/capabilities jsou prázdné. Agent version je z linker build metadata.
- Stejné vstupy mají unchanged plan. Different valid existing file je conflict,
  invalid/expired/unsafe existing state se odmítne a nezmění. Žádný --force,
  backup disposal, recovery, automatická rotace nebo přepis ručního YAML.
- Náhled musí jít úspěšně vypsat před textovým/interactive write. V JSON apply
  jde o jeden výsledný dokument po potvrzené operaci, ne streaming preview.
- Revalidate TLS po potvrzení používá čerstvý clock snapshot; expirace během
  promptu zabrání Apply. Není to promise budoucí neexpirace ani auto-renew.
- Apply má signal cancellation a používá stale/atomic/backup kontrakt P04b.
  Při hard crash nebo transakční chybě může zůstat recovery state; chyba není
  tvrzení, že v tom případě nic nebylo zapsáno. Doctor --setup-dir jej může
  read-only inspect, automatic recovery/discard tento CLI nenabízí.
- Při selhání output po dokončeném apply zůstává dokončený setup na disku;
  před opakováním ověřte stav, nevyvozujte rollback z chybějící stdout zprávy.
- Výstup používá fixed codes a names/modes; žádné raw parser/OS errors,
  key data, YAML/environment nebo secret contents. File paths jsou options,
  ne místo pro zadání key material do argv.

Exit 0 preview/applied/cancel, 1 validation/account/TLS/transaction failure,
2 usage/output failure. JSON obsahuje status/read_only/code/preview/result podle
fáze. TRANSACTION_INCOMPLETE má read_only=false, aby nezakrýval možné effects.
Native provisioning/readers jsou Linux-only; Windows má offline help/test doubles,
nikoli native instalaci. OS NSS identity lookup má vlastní OS timeout/provider.

## Evidence a další práce

Fake Host tests: help/usage/non-TTY bez reads, opt-in apply, interactive cancel
a bounded input, secret-free errors, output failure před text apply a expirace
po potvrzení. Linux native CLI test: preview bez writes, skutečný confirmed
Apply, repeat changed=0 při backup retention, resources [], zachovaný journal
a odmítnutí public-readable private key input.

Zbývá directory provisioning, controller env/policy/tokens/allowlist/CLI wizard,
explicitní unit/start/enable orchestrace a booted VM acceptance. Žádná implicitní
node admission, service activation nebo package install není tímto commandem
schválená/provedená. P05 ani release checkpoint B tím nejsou dokončené.

Lokální evidence 2026-10-14: Windows full test/vet a Linux full race/test/vet
prošly. Skutečné agent setup --help a původní --version vstupy prošly offline
bez konfigurace. Linux arm64 onboarding test binary compile-only prošel
(ne live ARM/TTY/booted VM acceptance). git diff --check čistý, VM beze změn.
