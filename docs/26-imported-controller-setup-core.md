# P05d — Imported controller setup jádro

Interní knihovna od 2026-10-14. CLI hlcp-controller setup ani automatická
generace tokenů ještě nejsou součástí tohoto bloku; preview.4 se nemění.
Navazující P05e je implementuje v [controller CLI](27-imported-controller-setup-cli.md).

## Vstupy a bezpečné defaults

PlanController přijímá explicitní target directory/Owner, ListenAddress,
ServerName, AdmittedNodes, ImportedTLS a ControllerCredentials.
ImportedTLS má P05a server cert-chain/key, vlastní issuer trust a oddělený
agent peer trust; nemusí to být stejná CA. Validuje SAN jméno/IP, explicitní
server-auth, key match, leaf/CA/validity/chain/pathLen. Žádný CA private key.

ListenAddress musí být literal IPv4/IPv6, nikoli hostname, empty host, wildcard
0.0.0.0/:: nebo injection text. Port 1024–65535, explicitní canonical numeric
formát; default service má prázdné capabilities. ServerName má DNS/IP syntax
a musí jej krýt server leaf. Nenahrazuje to kontrolu skutečné IP konfigurace,
reachability, firewall ani dostupnosti listeneru.

AdmittedNodes je explicitní seznam nejvýše 1024 validních unikátních identit,
deterministicky seřazený. Default [] => deny all nodes, nikoli legacy CA-only
trust. Pouze callerem dodané identity jsou v plánovaném admission souboru.
Tím se během plánování/APPLY nezaregistruje ani nekontaktuje agent.

ControllerCredentials importuje dva různé validní privátní API token snapshoty.
Normalizuje stejný koncový newline jako runtime LoadToken, kopíruje data a
JSON/String/GoString je redigují. Žádný RNG, nové credentials nebo rotace při
preview. Tento core tedy není příkaz k tomu, aby uživatel vyráběl tokeny ručně:
navazující wizard přidá jejich tvorbu až po potvrzení a reuse při opakování.

## Transakční soubory

| Soubor | Význam |
|---|---|
| controller.crt / controller.key | Server leaf chain a jeho privátní klíč |
| ca.crt | Runtime agent trust |
| issuer-ca.crt | Lokální vlastní issuer trust evidence, ne runtime peer trust |
| controller.env | Jediná HLCP_AGENT_LISTEN assignment, bez tajemství |
| api-policy.json | Existující standardní operator/observer policy, fixed relative token names |
| api.token / observer.token | Dva odlišné private bearers |
| nodes.json | Explicitní admission snapshot, default prázdný |

Devět souborů, všechny mode 0600. Používá se společný privátní file/directory
setup plán P04/P05c: preview bez writes, Apply s consent, safe-directory creation,
atomic no-clobber file batch a retained backup. Stejné vstupy => unchanged,
different valid existing config/token/admission => conflict, ne replace.
Invalid/unsafe existing state se zachová. Žádný force/rotation/discard/recovery.
Textové komentáře controller.env lze validovat, ale liší-li se obsah od candidate,
jsou zachované jako conflict, nikoli implicitní přepsání.

První create pod standardním service profilem vyžaduje /etc/hlcp/controller
s root ownership; custom directory v interním API nepřepíše hardcoded cesty
distribuované unit. CLI bude muset profil/destination svázat explicitně.
Core nikdy nepřipravuje/mění service account, units, firewall, ledger, journal,
user CLI config ani credential kopie do uživatelského home. Policy token paths
jsou relativní a odpovídají systemd LoadCredential snapshot names.

## Sdílená validace, ne paralelní security schema

localapi.ParseToken/ParsePolicy a transport.ParseAllowedNodes validují bounded
memory snapshots. Runtime LoadToken/LoadPolicy/LoadAllowedNodes je používají
po původních OS/credential checks. Policy zachovává strict unknown/duplicate JSON
checks, role/scopes/identity a duplicate bearer rejection; reader callback v
onboarding povoluje pouze api.token/observer.token. Žádné čtení libovolné cesty
z předložené policy. Runtime relative/absolute resolver nadále zůstává původní.
LoadPolicy nepředává raw token errors a Policy neuchovává bearer plaintext.

## Evidence / další práce

Windows full test/vet a Linux full race/test/vet prošly. Native controller test
ověří preview bez directory, no-consent refusal, create devíti souborů,
načtení skutečným runtime LoadPolicy/LoadToken/LoadAllowedNodes, default deny-all,
repeat unchanged, refusal/preservation při token změně a private modes.
Explicitní admission se ukládá v canonical pořadí, duplicates se odmítají.
Snapshot parser tests pokrývají duplicates/trailing/oversize a invalid credentials;
privátní material a plan formatting jsou redigované. Arm64 compile-only prošel,
ne live ARM nebo booted VM/service acceptance.

Navazuje skutečný controller CLI/TTY wizard: bezpečné bounded input reads,
preview bez credential generation, post-consent nové random tokens/reuse,
TLS recheck před write, user CLI provisioning se samostatným account consent,
explicitní service account/unit/start/enable a reboot acceptance.
Imported externí obnovy zůstávají externí; nynější TLS a LoadCredential jsou
restart-only snapshoty. Tento core nezavádí managed issuer/renewal ani nový release.
