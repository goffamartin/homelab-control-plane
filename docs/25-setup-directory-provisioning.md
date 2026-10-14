# P05c — Potvrzené vytvoření posledního config adresáře

Ve zdrojích od 2026-10-14; preview.4 se nemění.
hlcp-agent setup již nevyžaduje existující finální config directory.
Jeho rodič a předci ovšem musí bezpečně existovat. Toto není obecné mkdir -p,
recursive home bootstrap nebo oprava cizích/existujících permissions.

## Preview a consent

Imported cert/key/options se validují před plánováním adresáře. Chybí-li pouze
poslední adresář, PlanDirectory otevře bezpečný parent descriptor, ale nic
nevytvoří. File plan se zatím validuje přes read-only absent source.
Preview má změnu configuration-directory/create-directory/0700 a pět souborů
z P05a. Sám nic nezapisuje, včetně lock/staging/backup adresářů.

Apply vyžaduje stejné explicitní potvrzení jako soubory. PlanDirectory.Create
odmítne no-consent, canceled context, closed/used plán, parent rebinding a
kolizi s jiným procesem. Nepřevezme ani neopraví právě se objevivší cíl.
Identity pro nové dirs musí odpovídat skutečnému caller UID/GID a registrovanému
primary GID; root tu neprovádí implicitní provisioning pro jiný účet.
Existing-dir P04 mechanismus zůstává beze změny.

## Linux publish

- Clean absolute path <=4096, žádný root target nebo NUL, descriptor-relative
  O_DIRECTORY/NOFOLLOW průchod každým existujícím parentem.
- Trusted owner root/caller, žádné group/other write kromě root-owned sticky
  ancestorů (např. /tmp). Umístění a permissions se ověří znovu před publish.
- Nový soukromý staging directory v pinned parentu. Chown/chmod se týkají jen
  tohoto nově vytvořeného staging inode, nikdy existujícího cíle. Mode 0700,
  validated primary group a odstranění inherited setgid.
- Staging sync, identity/mode/owner recheck a Linux renameat2 NOREPLACE:
  žádný overwrite fallback. Post-publish binding + directory/parent fsync.
- Poté fresh planner na pinned novém directory znovu Build ověří všech pět
  očekávaných create entries. Existující entry v této fázi => stale/conflict,
  ne nové implicitní reuse. Samotné soubory pak zapisuje P04b transakce.

Linux amd64/arm64 mutation ABI; jiná platforma nemá native create fallback.
Nejde o ochranu proti útočníkovi s root/stejným UID ani absolutní atomicity proti
současným directory-tree permission změnám. Restriktivní owner-bit umask, který
znemožní otevření nového staging inode, způsobí fail a zachování pro inspekci,
nikoli globální změnu umask. ACL/noexec/NFS/power-loss guarantees se tím nezavádějí.

## Selhání, recovery a velikost state

Creation directory a P04b file batch nejsou jedna globálně atomická transakce.
Po publish/interrupt může zůstat nový private prázdný target; nikdy se automaticky
rekurzivně nemaže. Po file apply failure se zachovají P04b recovery/backup data.
Opakovaný CLI plán nad takovým targetem používá normální existing-dir kontroly.
result.changed počítá soubory (typicky 5), nikoli directory change.

Staging jméno je pevně odvozené z target basename SHA256 v jeho parentu:
.hlcp-directory-<digest>. Na jeden cílový basename je tedy nejvýše jeden
automaticky vytvořený staging entry; žádné opakované náhodné orphan dirs.
Pokud existuje již při plánování/creation, další pokus fail closed
(DIRECTORY_RECOVERY_REQUIRED); neadoptuje jej, neupravuje a nevytváří další.
Hard crash před publish může staging zanechat. Tato CLI zatím nemá jeho
automatickou recovery/discard operaci; operátor musí stav nezávisle prověřit.

Běžné selhání před publish může odstranit pouze právě vytvořený známý,
identity/owner/mode-verified prázdný staging directory pomocí rmdir (žádná
recursive deletion). Jiný inode, změněná metadata, neznámý obsah nebo unsafe
parent se zachovají. Jestli cleanup není prokazatelně bezpečný/úspěšný, příští
pokus blokuje existující staging; hlášení není příslib úplného rollbacku.

Journal se nadále ani nečte ani nevytváří, resources jsou prázdné a služba/admission
se neaktivují. Chybějící parent má PARENT_DIRECTORY_REQUIRED; chybějící state
directory se v této fázi také nevytváří. Uživatel může zvolit config directory
pod existujícím home nebo si předem připravit obvyklou .config strukturu.

## Evidence a otevřené bloky

Testy: preview bez writes, consent/cancel, private mode, create once, kolize před
create/publish bez overwrite nebo chmod cizího obsahu, parent rebinding,
nonrecursive/symlink refusal, interrupted publish, orphan block a neznámý obsah
staging zachovaný. Native CLI test nově začíná bez target directory, ověří
preview, potvrzené vytvoření + pěti souborů, repeat changed=0 a journal preservation.

První full Linux gate odhalila časově citlivý již existující script cleanup test.
Test helper byl stabilizován v samostatném commitu: po ověřené absence/zombie
stavu původního procesu neprovádí druhé popírající /proc čtení a cleanup se
nepokouší killnout znovu použitý PID. Product script executor se nezměnil.
Poté tento test prošel 30 opakováními a Linux full race/test/vet prošly.
Windows full test/vet prošly. Linux arm64 setup/onboarding compile-only prošel,
ne live ARM nebo booted homelab VM acceptance. Žádný nový preview.

Další P05 blok: controller provisioning/průvodce, explicitní service orchestrace,
případný samostatný bezpečný parent bootstrap a konkrétní VM acceptance.
