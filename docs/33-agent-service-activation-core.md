# P05k — Agent service activation core a system credentials

Interní zdrojové jádro od 2026-10-14. Zatím bez native agent systemd adaptéru
a bez příkazu `hlcp-agent activate`. Nespouští služby na VM; preview.4 se nemění.
P05/checkpoint B a live booted-systemd/reboot acceptance zůstávají otevřené.

## Oprava system setup

Agent setup nyní předává Scope do PlanAgent. User scope zachovává cert/key/CA
cesty v explicitní private config directory. System scope místo nich zapisuje:

```text
/run/credentials/hlcp-agent.service/agent.crt
/run/credentials/hlcp-agent.service/agent.key
/run/credentials/hlcp-agent.service/ca.crt
```

Distribuovaná unit používá účet hlcp a systemd LoadCredential. Ten nemůže přímo
číst mode-0600 soubory v root mode-0700 provisioning directory; původní generic
factory cesty tedy nebyly vhodné pro jeho runtime. Skutečné importované soubory
nadále vznikají v explicitním target directory; issuer-ca.crt zůstává lokální
evidence pro validaci, není runtime peer trust ani CA privátní klíč.

Scope system vyžaduje Owner UID/GID 0, scope user nerootové UID/GID. CLI ověřuje
tuto hranici před TLS reads. Interní Scope prázdný zachovává předchozí generic
file-planning API; veřejné setup CLI má vždy explicitně vybraný/default user scope.
Žádný overwrite existující konfigurace: starý odlišný obsah se zachová jako
konflikt a vyžaduje samostatnou migraci.

Directory a journal stále volí caller. Neznamená to přepsání standardní unit:
system native adapter musí vyžadovat /etc/hlcp/agent provisioning a standardní
/var/lib/hlcp/command-journal.json state cestu/sandbox, nebo podporovat výslovně
jiný prověřený profil. Setup neotevírá credential runtime kopie ani journal.

## Povolené service profily

| Scope | Unit | Caller |
|---|---|---|
| system | hlcp-agent.service | root UID/GID 0 |
| user | hlcp-agent.service | skutečný nerootový runtime owner |
| user | hlcp-agent-podman.service | stejný owner, compatibility name |

Všechno ostatní se odmítne před host inspection. Žádný arbitrary unit, remote
manager, root impersonation nebo HOME/bus environment inference. Embedded agent
unit templates používají přesně stejné substitutions jako Debian packaging,
takže budoucí adapter nebude udržovat druhou ručně opsanou security konfiguraci.

## Plán a consent

PlanAgentActivation používá společné již testované P05f lifecycle jádro:
start pouze inactive, enable jen explicitně a pokud chybí, active no-op bez
restartu, snapshot recheck, single-use confirmed plan a partial outcomes.
Žádný stop, reset-failed, implicitní enable/linger, repair či retry.

User scope navíc vyžaduje, aby druhá agent unit v tomtéž user manageru byla
inactive/disabled, nebo opravdu absent/absent. Active, failed, transitioning,
enabled či enabled-runtime peer je refusal. Nevyužije se Conflicts jako způsob,
jak automaticky zastavit jiného pisatele stejného journalu. Přechod mezi unit
názvy vyžaduje samostatně schválené zastavení/disable a migraci.

Při výslovném persistent enable user služby musí již existovat linger. Jinak
controlled refusal, žádný root loginctl zásah. Bez enable je možné plánovat
session-local start bez linger; to není záruka chodu po logout/reboot.

Caller, peer a linger evidence se přidávají do immutable proof i tehdy, pokud je
host původně do svého proof nezahrnul. Dynamic active/enabled vybrané služby se
nehasheují: právě ty se mají při schváleném enable/start změnit. Source metadata
změna před akcí je stale; neověřený post-check znamená uncertain partial result.
Po potvrzeném enable a neúspěšném start zůstává enable zachovaný, bez rollbacku.

## Kontrakt navazujícího native adaptéru

Host není CLI/user-supplied evidence. Musí pokaždé validovat skutečný účet,
installed binary a fixed unit obsah/paths/drop-ins, cílový manager a jeho stav,
owned config/TLS snapshots a imported issuer/peer trust, node URI/key/chain/
expiry, runtime credential paths, journal cestu a dostupnost potřebných lokálních
runtime práv. Preflight nesmí otevřít journal, spustit resource command ani
kontaktovat controller. System service runtime účet hlcp je jiný než root caller.

User native adapter musí běžet přímo pod ownerem se skutečným user manager/bus,
kontrolovat peer i linger a používat bounded fixed systemctl --user; system
adapter jen fixed system manager. Profily v jiných uživatelských/system managerech
nejsou automaticky koordinované. Operator nesmí spouštět stejnou node/journal
identity souběžně; journal lock je dodatečná runtime ochrana, nikoli migrace.

Případné native enable/start je samostatný výslovný krok: může otevřít journal,
navázat mTLS, registrovat agent a spustit jeho běžné observations. Nejde o file
transaction a není atomický proti souběžné správě systému. Resource configuration
zůstává explicitní; tento core nic nepřidává ani nepovoluje.

## Evidence / další práce

Windows full test/vet a Linux full race/test/vet prošly. Profile tests zahrnují
všechny tři manager/unit volby, owner gating před inspection, consent, ordering,
peer/linger refusal, safe absent peer, session start bez implicitního enable,
stale caller/peer/linger/proof a partial failure bez retry. Native system setup
test ověřuje skutečný generovaný YAML LoadCredential paths a zachování journalu;
template tests ověřují package substitutions a user mapping-helper compatibility.
ARM64 compile-only prošel; nejsou to live systemd/Podman/ARM/reboot testy.

Navazuje native agent preflight/activation CLI, service/account/state readiness,
explicitní legacy profile migrace a fresh Debian package/VM acceptance checkpointu B.
