> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05i — Potvrzené předání přístupu do user CLI

Implementováno ve zdrojích od 2026-10-14. Nový příkaz není v publikovaném preview.4;
nejde o update běžících VM. P05/checkpoint B zůstává otevřený.
Navazující [P05j](32-client-config-parent-bootstrap.md) doplňuje potvrzené vytvoření
chybějící `.config`; níže je tento rozšířený aktuální kontrakt.

## Příkaz

Na controller hostu s odpovídajícím instalovaným Debian profilem:

```sh
# Náhled, bez odeslání bearer tokenu uživatelskému procesu.
sudo /usr/bin/hlcp-controller configure-client --user martin

# Samostatné potvrzení exportu EXISTUJÍCÍHO operator přístupu a zápisu profilu.
sudo /usr/bin/hlcp-controller configure-client --user martin --apply --yes

# Alternativně již existující observer přístup (čerstvý cílový profil).
sudo /usr/bin/hlcp-controller configure-client --user reader --role observer --apply --yes
```

Výchozí role je operator. `--role` vybírá existující token, nevytváří nové ACL,
identity, oprávnění ani rotaci. Uživatel, kterému jej předáš, získá stejný přístup
jako příslušný zdrojový bearer; sdílení tokenu není per-user identity/audit/revocation.
Role observer omezuje API podle existující policy, nikoli podle tohoto průvodce.
`--json` je redigovaný report; help a chybná syntax nečtou host ani soubory.

Vyžaduje root UID/GID 0, běh důvěryhodné root-owned ELF binárky právě z
`/usr/bin/hlcp-controller`, explicitní nerootový účet a bezpečný již existující
home. Chybějící `.config/hlcp` se vytvoří až po potvrzení. Profil vzniká v home pod
`.config/hlcp`; žádný HOME/SUDO_USER/XDG odhad. Vlastní/XDG cesty a automatická
tvorba jiných parent directories nejsou podporované. Pro standardní default
prostředí pak vybraný uživatel použije `hlcp -n` bez sudo/token argumentů.

Existující profil vytvořený starším `hlcp configure` s jiným token filename nebo
formátováním může být konflikt: zůstane funkční a nedotčený. Automatická migrace,
adopce externích token paths, overwrite či permission repair se neprovádějí.

## Hranice root / user

1. Parent validuje výhradně controller-owned zdroj: privátní api-policy.json,
   api.token a observer.token, unikátní validní bearers, požadovanou skutečnou role
   ve sdíleném immutable Policy snapshotu a nepoškozený setup transaction stav.
   Token path resolver připouští jen tyto dva fixed názvy; nečte libovolné paths
   z policy. Zdroj se nemění a tokeny se negenerují.
2. Cílový účet se validuje přes forward/reverse OS lookup, UID/GID, home a omezený
   seznam supplementary groups. UID/GID 0 či root supplementary group se odmítají.
   Parent žádné soubory v user home neotevírá, nechownuje a neopravuje.
3. Parent spustí pouze vlastní trusted installed binary s pevnou interní akcí,
   po nahrazení UID/GID/supplementary groups a se sanitizovaným prostředím.
   Žádný shell, user executable, obecný run-as, CLI token args nebo secret env.
4. Privátní pipe nejprve nese jen cílovou cestu a SHA-256 otisk normalizovaného
   beareru, nikoli bearer. Child validuje svou skutečnou identitu/default path,
   čte jen své soubory, připne P05h plán a vrátí bounded metadata preview.
   Fresh preview používá neprivátní schema stand-in, který se nikdy neaplikuje.
   Existing token se musí shodovat s otiskem; konflikt není důvodem k rotaci.
5. Bez `--apply --yes` parent zavře pipe. Child skončí bez zápisu a token mu není
   odeslán. Po potvrzení parent znovu ověří source snapshot, účet i binárku.
6. Teprve poté bearer odešle přes anonymous stdin pipe. Child zkontroluje otisk,
   původní plán a identitu, vytvoří final intents se skutečným tokenem a požaduje
   stejný preview change set. Použije původní pinned directory/transaction plan;
   raced target nepřevezme. P05h token/profile zápis má user ownership a mode 0600.

SHA otisk není nový token ani veřejný report; při preview jej dostane vybraný
user child jako credential metadata. U slabého uživatelem zvoleného beareru
umožňuje offline hádání, proto používat kryptograficky náhodné zdrojové tokeny.
Token může cílový uživatel po výslovném předání samozřejmě číst/sdílet: není to
ochrana před samotným autorizovaným vlastníkem ani jeho procesy/debuggerem.

Zdrojová role a scopes jsou validované podle diskové policy, nikoli potvrzením
živého API. Aktivní controller může mít starší startup/LoadCredential snapshot;
jeho explicitní restart/ověření aktuální policy zůstává oddělenou operací.
Tento příkaz neprovádí network request, nezjišťuje live role, nespouští služby,
nečte TLS/CA privátní klíče ani ledger/journal. Změna zdroje po poslední kontrole
je race s privilegovanou správou, nikoli globálně atomický credential export.

## Chyby a limity

Celý child process má 20s timeout, process-group cancellation a omezený pipe wait.
Vstupní protokol je nejvýše 4096 bajtů; stdout report nejvýše 16 KiB a stderr
má samostatný 16KiB buffer. Parent neforwarduje raw stderr/errors ani arbitrary
child text: preview má jen fixed názvy cli.json/cli.token/configuration-directory
a povolené action/mode kombinace, final result jen ověřovaný schema/range.

Před exportem jsou refusal reports read-only (ROOT_REQUIRED, INSTALLED_PROFILE_REQUIRED,
ACCOUNT_REFUSED, SOURCE_REFUSED, USER_PROFILE_REFUSED, CHILD_REFUSED, STALE_SOURCE).
Po zahájení exportu neznámý či neověřený child výsledek znamená
TRANSFER_INCOMPLETE a `read_only: false`. Credential již mohl být předán a soubory
mohly vzniknout; timeout/kill ani chyba výstupu nejsou důkaz non-execution.
Žádný automatický retry, reset, force, recovery, discard backup ani rollback.
Nejdřív inspekce user profilu pod skutečným uživatelem a controller source.

Exit 0 preview/applied, 1 bezpečnostní/procesní/transaction refusal, 2 syntax
nebo output failure. Neúplné `.config/hlcp` i jiné existující nastavení se zachová.
Dokončený file batch je oddělený od credential exportu a directory creation;
není to jedna atomická transakce přes dva účty.

## Evidence / otevřené brány

Windows full test/vet a Linux full race/test/vet prošly, následné cílené kontroly
pokrývají final změny. Izolovaný offline Linux kontejner s opravdu instalovanou
controller binárkou a skutečným nerootovým účtem ověřil celý native parent/child
workflow: preview bez files, create s user ownership/private modes, repeat 0
changes, secret-free output a odmítnutí role změny bez přepsání přístupu.
Source tests ověřují role matching, stale source a zakázané externí token paths;
CLI testy ověřují offline help, invalid options a samostatný consent.
ARM64 compile-only prošel, nikoli live ARM/systemd/reboot acceptance.

Bootstrap známé dvojice `.config/hlcp` navazuje v P05j; zbývá explicitní migrace
stávajících profilů podle potřeby, agent service integrace a booted Debian VM
acceptance před release checkpoint B. Managed PKI/renewal a distribuční helper
zůstávají pozdějšími etapami schváleného plánu.
