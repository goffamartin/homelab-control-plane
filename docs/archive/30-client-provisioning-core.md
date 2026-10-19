> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05h — Privátní user CLI provisioning jádro

Interní zdrojové jádro od 2026-10-14. Zatím nejde o nový uživatelský příkaz ani
automatický root-to-user přenos tokenu. Tyto části navazují samostatně; preview.4
a běžící homelab se nemění, P05/checkpoint B zůstává otevřený.
Navazující [P05i](31-client-access-handoff.md) přidává potvrzený native transfer
a příkaz configure-client; tento dokument popisuje samostatné provisioning jádro.

## Sdílený runtime kontrakt

`cli.ClientConfig` / `cli.ParseClientConfig` centralizují existující version-1
schema. Runtime `hlcp` používá stejný parser po původních file/permission checks.
Parser je bounded na 8192 bajtů, odmítá duplicate/unknown fields, trailing JSON,
nesprávnou verzi, prázdný token path a non-loopback API. Relativní token path
se resolve proti adresáři `cli.json`; absolutní cesty starých profilů zůstávají
podporované. Nečte token, soubory, síť ani environment a neuděluje oprávnění.
Existující `hlcp configure` zůstává funkční a nezískává implicitní overwrite.

`ClientCredential` přijímá již autorizovaný bearer přes shared ParseToken,
kopíruje/normalizuje snapshot a rediguje JSON/String/GoString. Nevytváří nový token,
neprovádí RNG, nerozhoduje o role/scope a nemění controller policy. V tomto core
ověření formátu tokenu není důkaz, že jej controller přijme. Budoucí parent musí
ověřit zdroj/role v aktuální controller policy před výslovně schváleným předáním.

## PlanClient

Caller dodá explicitní directory, Owner, loopback API a ClientCredential. Musí
již běžet pod cílovým nerootovým účtem se skutečným UID/GID odpovídajícím OS
registraci a primary group. Root, jiný účet/GID či nepodporovaný host se odmítají
před čtením user target. Identita se ověřuje i před Apply.

Plán obsahuje pouze:

| Soubor | Obsah |
|---|---|
| cli.json | version 1, loopback API, relativní api_token_file: cli.token |
| cli.token | Kopie již autorizovaného bearer snapshotu |

Oba mode 0600, cílový adresář 0700. P04/P05c pinned planner, čistý preview,
confirmed directory creation a transactional file batch zůstávají společné.
Bez chown, opravy oprávnění, rekurzivních parents, root manipulace s uživatelskými
paths nebo HOME/SUDO_USER/XDG inference. Caller musí vybrat skutečnou default
user config cestu, pokud chce následné automatické nalezení přes `hlcp -n`.
Bezpečný rodič musí existovat; tvorba chybějící `.config` je navazující orchestrace.

Plán používá jediný připnutý file snapshot: oba files absent => create, oba
unchanged => repeat bez rotace/zápisu. Mixed create/unchanged => partial pair
refusal. Odlišný či invalid existing obsah se zachová jako konflikt/invalid state,
ne jako fresh installation. No-force; token se nikdy automaticky nevymění.
Původní `hlcp configure` profil s jiným token filename/formátováním může být
konflikt; jeho migrace/adopce není tímto core implementována.

Chybějící finální directory vznikne až po Apply consent pod původně připnutým
rodičem. Raced directory se nepřevezme. Žádný ledger, journal, služby, listeners,
resources, account changes nebo credential kopie certifikátů/CA klíčů.
Directory creation a file batch nejsou jedna globální atomická operace; selhání
vyžaduje inspekci a nepovoluje automatický force/recovery/discard backup.

## Ověření a další hranice

Windows full test/vet a Linux full race/test/vet prošly. V jednorázovém offline
kontejneru byly native testy spuštěny se skutečným nerootovým účtem, bez elevated
file writes: preview bez files, no-consent, fresh create/modes, shared runtime
parse/token load, repeat unchanged, token konflikt a preservation, partial pair,
unsafe target, wrong primary GID a raced directory. Parser tests pokrývají
strict/bounded JSON a legacy absolute paths; credential/plan report je redigovaný.
ARM64 prošel compile-only. Nejde o booted-systemd, live ARM, vzdálené nasazení
ani dokončený transfer workflow.

Další blok musí výslovně vybrat cílový účet a přístup, validovat controller source
a trusted installed child executable, dropnout UID/GID/supplementary groups před
dotykem user souborů a předat bearer jen bounded privátním kanálem, ne args/env/
stdout. Preview i potvrzení musí zachovat stejné snapshot/consent hranice; při
neznámém child výsledku nehádat non-execution a automaticky neopakovat zápis.
Poté agent service integrace a skutečná Debian VM/reboot acceptance checkpointu B.
