> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05p — Vysvětlení systémového resource preflightu

Stav: statická analýza a redigované odmítnutí, nikoli runtime sandbox probe.
System-scope agent s resources zůstává odmítnutý. User-scope metadata kontroly
z P05n a system-scope bez resources se nemění. Preview.4 zůstává beze změny.

## Proč nestačí kontrolovat jako root

Pevná systémová jednotka běží jako `hlcp:hlcp` s NoNewPrivileges, ProtectHome,
PrivateTmp a ProtectSystem=strict. Root při provisioning vidí jiný filesystem
a má jiná práva. Kontrola skriptu jako root proto není důkazem, že ho agent
uvidí či provede. Žádné automatické sudo, grants, drop-ins ani oslabení sandboxu.

## Co report nově vysvětluje

`hlcp-agent start --scope system` po úspěšném config/TLS/profile preflightu
nadále vrátí RESOURCE_PREFLIGHT_REQUIRED. U resources navíc vrací pole
`constraints` v JSON, nebo `Constraint: <code>` v textu:

| Kód | Význam |
|---|---|
| system.sandbox_probe_required | Skutečné ověření pod účtem a sandboxem služby zatím není implementované |
| system.profile_contract_changed | Distribuovaný profil již neodpovídá předpokladům analyzátoru; nutná nová analýza |
| system.docker_group_not_provisioned | Generic profil nepřidává Docker supplementary group; žádný automatický root-equivalent grant |
| system.podman_privileged_helpers_blocked | NoNewPrivileges brání zvýšení oprávnění standardních setuid/capability helperů; rootless user profil má jiný kontrakt |
| system.systemd_authorization_unverified | Neověřená skutečná autorizace resource volání, bez předpokladu polkit grantů |
| system.script_protected_home | Executable/workdir textově míří do /home, /root nebo /run/user, které služba skrývá |
| system.script_private_tmp | Executable/workdir míří do /tmp nebo /var/tmp; služba nepoužívá hostův obsah těchto adresářů |
| system.script_read_only_workdir | Workdir není pod StateDirectory ani privátním tmp; nelze předpokládat, že tam skript může zapisovat |
| system.script_dependencies_unverified | Soubory, interpreter a ostatní závislosti skriptu nejsou ověřené ve skutečném sandboxu |

Jde o omezení/předpoklady, ne důkaz, že každá uvedená akce musí selhat. Například
skript může v read-only workdir fungovat, pokud nezapisuje. Neanalyzujeme jeho
obsah, argv/environment nebo symlink targets, nezkoušíme runtime akci ani přístup
k socketu. Ani nenalezení path konfliktu neopravňuje aktivaci.

## Bezpečnostní a testovací kontrakt

Analýza probíhá po strukturální validaci configu, je bounded podle core resource
limitů a nevolá žádný host probe. Pevný embedded system profil se kontroluje vůči
expected security directives; duplicates, supplementary groups a nové neznámé
service directives vedou k controlled refusal, ne tiché změně předpokladů.
Aktuální installed unit/drop-ins už předtím kontroluje native activation host.

Kódy jsou deduplikované, seřazené a nemají resource IDs, paths, args/env nebo OS
chyby. Error zachovává errors.Is pro původní refusal; veřejné formátování je
redigované a kolekce se vracejí obrannou kopií. CLI report zůstává read_only=true,
bez start/enable, bez grants a bez filesystem writes. Constraints jsou doplňující
pole chybového reportu, nikoli nový permission/config profil.

Testy ověřují skutečnou embedded jednotku, změnu sandbox directives, nové mount/
environment directives, path component boundaries, deterministické kódy, redakci,
obranné kopie, nulové host inspection a CLI text/JSON refusal i při --apply --yes.
Windows full test/vet, Linux full race/test/vet a Linux ARM64 compile-only jsou
lokální evidence; nejde o booted systemd/VM nebo sandbox execution akceptaci.

Navazuje explicitně navržený, bounded read-only probe skutečného sandboxu. Ten
nesmí být schovaný v čistě read-only preview, pokud vytvoří transient unit či
state/credential namespace. Bude potřebovat samostatný jasný souhlas a lifecycle
kontrakt, potom skutečnou VM akceptaci. P05/checkpoint B zůstává otevřený.
