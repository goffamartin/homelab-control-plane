# P05j — Fresh-user bootstrap .config/hlcp

Zdrojová implementace od 2026-10-14. Configure-client už nevyžaduje předem
vytvořené `.config`; publikuje pouze přesně známou dvojici `.config/hlcp`
v existujícím bezpečném registrovaném home. Preview.4 a běžící VM se nemění.

## Preview a potvrzení

Stejný příkaz [P05i](31-client-access-handoff.md), žádný nový přepínač:

```sh
sudo /usr/bin/hlcp-controller configure-client --user martin
sudo /usr/bin/hlcp-controller configure-client --user martin --apply --yes
```

Pokud chybí `.config`, náhled má čtyři změny: user-config-directory a
configuration-directory (oba create-directory 0700), cli.json a cli.token
(oba create 0600). Žádné skutečné directory/files, bearer export nebo oprava
oprávnění při preview/zrušení. Public schema stand-in se nikdy neaplikuje.

Pokud `.config` existuje a je bezpečný rodič, zachovává se i jeho mode (například
0755). Hlcp podadresář se musí stát/remain privátní 0700. Existující writable,
symlink nebo neplatný target vede k refusal, ne chmod/chown/repair.
Chybějící home ani jiné řetězce parent directories se nevytvářejí. Custom XDG,
legacy profile migrace a výběr jiné config cesty nejsou tímto doplněny.

## Publikace po předání tokenu

Zápis stále provádí credential-dropped user child po samostatném consent,
parent source/account/binary recheck a privátním bearer transferu. Root žádné
user paths neotevírá ani nezapisuje. Final file intents používají skutečný token
a musí mít stejné změny jako původní file preview.

1. PlanDirectory(.config) připne existující bezpečný home už během preview.
   Po potvrzení vytvoří/publikuje .config přes P05c private staging a NOREPLACE.
2. Create vrátí planner nad přesným nově publikovaným .config deskriptorem.
   `Planner.PlanChildDirectory("hlcp")` duplikuje tento původní descriptor atomicky
   s CLOEXEC, znovu kontroluje binding/owner a plánuje jen absent direct child.
3. Child Create opět ověří původní parent binding, final/stage absence a
   NOREPLACE publikaci. Není to nové otevření/adopce .config přes pathname.
4. Planner nad nově vytvořeným hlcp directory připraví file batch. Musí to být
   původně odsouhlasené dvě create akce, nikdy adopted existing files. Zápis
   zůstává P04 private file transakce s retained backup a fsync hranicemi.

Každý deskriptor má jasné vlastnictví/Close. Child directory plán zůstává použitelný
i po Close původního parent planneru díky nezávislému CLOEXEC descriptoru.
Closed/unsupported planner, invalid child name, parent rebinding, raced entry
nebo neznámý staging se odmítají. Žádný arbitrary recursive mkdir či force.

## Částečné výsledky

Directory publikace a file transaction nejsou jedna globální atomická operace.
Po selhání může zůstat nová .config nebo .config/hlcp; neprovádí se automatické
mazání těchto publikovaných složek ani cizího obsahu. Post-export failure nadále
znamená TRANSFER_INCOMPLETE/read_only false a nejdřív inspekci pod cílovým účtem.
Nové spuštění plánuje skutečný stav, nikoli implicitní reset/retry starého plánu.
Tokeny, role/policy, ledger/journal, služby a account se tímto krokem nemění.

## Evidence a další práce

Windows full test/vet a Linux full race/test/vet prošly. Native testy spuštěné
pod skutečným nerootovým účtem ověřily čtyřpoložkový preview, no-consent bez writes,
skutečný token místo stand-in, private directory modes, repeat unchanged,
directory/symlink/home rebinding refusal a zachování existujícího parent mode.
Setup testy ověřují nezávislý descriptor po parent Close a refusal po rebinding.
Installed controller end-to-end test začíná opravdu bez user .config a ověřuje
celou root-to-user cestu včetně ownership/modes a repeat/role-conflict preservation.
Onboarding/setup ARM64 prošly compile-only; žádné live ARM/booted-systemd tvrzení.

Další bloky: explicitní legacy profile migrace podle potřeby, agent service
integrace, fresh Debian package/setup acceptance a skutečná VM/reboot brána
před checkpoint B. P05 ani nový release tímto nejsou uzavřené.
