# HLCP — řízení služeb v HomeLabu

Martin Goffa · ANI-COM · 18. listopadu 2026

[Zdroj kontroleru v1.1.0](https://github.com/goffamartin/homelab-control-plane/tree/v1.1.0) · [Zdroj měřeného agenta](https://github.com/goffamartin/homelab-control-plane/tree/720dcad18f0105f7fe65d33a1f55d4281a492a3c) ·
[Měřicí skripty a data](https://github.com/goffamartin/homelab-control-plane/tree/90b730d631fceba9b57f402b14ba6557e3ebad3b/evaluation/data/final-agent-720dcad) ·
[Anglická verze](report.md)

## Abstrakt

HomeLab Control Plane (HLCP) umožňuje správci z jednoho místa vyhledat kontejner, sledovat jeho výstup a provést povolenou akci. Systém jsme nasadili pro dvě aplikace. Na vyhrazeném kontejneru jsme změřili, jak dlouho trvá získat živý stav a provést restart. Přes HLCP dosáhly tyto operace mediánu 80,29 a 165,31 ms. U již navázaného SSH jsme naměřili 66,69 a 159,41 ms. Při jednom dotazu za sekundu jsme obdrželi všech 480 očekávaných odpovědí a agent využíval přibližně 7 % jednoho logického CPU.

## 1. Problém a výzkumná otázka

Jedna z našich aplikací běží ve třech kontejnerech, druhá v jediném kontejneru na stejném uzlu. Při hledání závady může správce vybrat kontejnery podle tagu aplikace a ve webovém rozhraní otevřít log konkrétní služby. Stejný inventář a povolené akce nabízí také CLI. Když správce požádá o restart, dostane ID příkazu, podle kterého může později dohledat výsledek.

Zajímalo nás, kolik času přidá cesta přes kontroler při zjištění stavu nebo restartu oproti SSH a co správce zjistí při výpadku agenta či zamítnutí požadavku. Na vyhrazeném kontejneru jsme měřili dobu provedení příkazu přes CLI. Rychlost webového rozhraní a zkušenost uživatelů by vyžadovaly samostatné měření.

## 2. Co jsme vyvinuli a nasadili

| Část systému | Co dnes umí | Kde jsme to ověřili |
|---|---|---|
| Kontroler a agent | Agent zveřejní uzel, jeho prostředky, tagy a povolené akce. Správce může například najít všechny kontejnery jedné aplikace. | Inventář nasazeného kontroleru vrátil pro jednu aplikaci tři kontejnery a pro druhou jeden. |
| Ovládání kontejnerů | Podle konfigurace lze vyžádat živý stav, spustit, zastavit či restartovat kontejner. Výsledek příkazu lze dohledat podle ID. | U běžící aplikace jsme přečetli stav přes CLI. Na testovacím kontejneru jsme nezávisle ověřili každý měřený restart. |
| Logy a webový klient | CLI přečte poslední řádky, umí je filtrovat a sledovat nový výstup. Web nabízí dashboard, detail uzlu a prostředku i sledování logu. | V CLI jsme sledovali průběžný výstup, přerušili čtení a po zastavení kontejneru znovu načetli posledních pět řádků. Čtení logů fungovalo i v nasazeném webu. |
| Instalace a správa | Balíčky pro Debian, párování uzlů, příkazy pro správu služby a doplňování v Bashi tvoří obsluhu nasazení. | Kontroler i agent po aktualizaci hlásily v1.1.0 a uzel se znovu objevil online. |

### Jak se provede jeden příkaz

Kontroler a agent jsou implementované v Go. CLI komunikuje s lokálním HTTP API
kontroleru. Agent navazuje trvalé spojení s kontrolerem a obě strany se
autentizují certifikáty pomocí mTLS. Aplikační protokol používá ohraničené rámce
s daty v CBOR [2, 3]. Inventář sděluje, co lze na uzlu ovládat. Provedení
operace dále podléhá pravidlům API a seznamu akcí konkrétního prostředku.

Kontroler předá strukturovaný příkaz s ID a agent spustí místní adaptér. Adaptér
Podmanu například zjišťuje stav pomocí `podman container inspect`. Záznamy
výsledků umožňují dohledat operaci po ztrátě odpovědi. Pád mezi provedením
externí akce a uložením výsledku může ponechat výsledek neurčený. Webový klient
používá stejné rozhraní kontroleru a do měření jeho režie nevstupoval.

## 3. Metodika

Měření latencí běželo na dvojici HomeLab VM s dvěma dostupnými logickými CPU
a 1 906 MiB systémem viditelné paměti na každé VM. Ve všech pěti experimentech jsme použili kontroler v1.1.0 (revize `0f11951`) a agenta v revizi `720dcad`, sestavené pomocí Go 1.27.3 pro Linux amd64. Na agentovi běžel Podman 5.4.2. Všechny varianty pracovaly se stejným rootless
kontejnerem pod stejným účtem na agentovi. Čas se měřil monotónními hodinami na
kontroleru od spuštění příkazu do jeho návratu.

### Příprava a pilot

Před sběrem dat jsme zkontrolovali verze a založili vyhrazený kontejner `hlcp-bench`. Pilot ověřil, že HLCP i SSH pod stejným uživatelem vidí běžící kontejner. Pro restart jsme před ostrým během provedli jeden pokus každou cestou.

### Co přesně jsme testovali

| Experiment | Postup | Jak jsme vyhodnotili pokus |
|---|---|---|
| E1 · Živý stav | Stejný kontejner jsme kontrolovali přes HLCP s trvalým spojením, nové SSH a již navázané SSH. Po 10 zahřívacích dotazech na variantu následovaly tři bloky po 100 dotazech na variantu. Skript v každém bloku promíchal pořadí. | Na kontroleru jsme měřili čas od spuštění CLI do návratu procesu. Poté jsme ověřili úspěch příkazu a odpověď `running`. Zahřívací dotazy jsme vynechali ze statistiky. |
| E2 · Restart | Každý ze tří bloků zahrnoval deset restartů každou cestou. Před pokusem skript počkal dvě sekundy a přes SSH přečetl ID, testovací značku, stav a čas startu kontejneru. | Časomíra běžela pouze během příkazu restart. Další nezávislé čtení přes SSH muselo najít stejný běžící kontejner s novým časem startu. |
| E3 · CPU a paměť | Na obou VM jsme po sekundě odečítali čítače cgroup služby. Provedli jsme tři párová 120sekundová měření v klidu a tři při jednom živém dotazu za sekundu. | Využití CPU jsme spočítali ze změny času CPU vůči uplynulému času. U paměti jsme pro každý běh uvedli medián vzorků. |
| E4 · Obnova spojení | Dočasný agent a kontroler běžely na jedné VM přes loopback. Pětkrát jsme agenta ukončili a znovu spustili. V druhé sérii jsme pětkrát pozastavili a obnovili stejný proces s otevřeným spojením. | Od přerušení jsme měřili dobu do zjištění stavu offline. Od nového spuštění či obnovení jsme měřili čas do nové relace a prvního úspěšného živého dotazu. |
| E5 · Pravidla a výsledky | Na dočasné instanci jsme jednotlivě vyzkoušeli chybný token, chybějící či nedůvěryhodný certifikát, nepovolený restart, konflikt ID, načtení výsledku a opakování požadavku. | Pro každý případ jsme porovnali skutečnou odpověď API nebo TLS s očekávanou. Každý případ jsme zopakovali na třech nezávislých dočasných instancích. |

Z jednotlivých pokusů jsme pro každou variantu spočítali počty úspěchů, chyb a timeoutů. Medián popisuje typický úspěšný pokus, p95 dobu, do níž doběhlo alespoň 95 % úspěšných pokusů. Souhrny stavu a restartů jsme přepočítali z řádků CSV po blocích i za celý běh. [Přehled běhů a dat](data/final-agent-720dcad/README.md)

## 4. Výsledky

### Živý stav a restart

| Úloha a způsob provedení | Úspěšné / celkem | Medián (ms) | p95 (ms) |
|---|---:|---:|---:|
| Stav · HLCP | 300/300 | 80,29 | 92,54 |
| Stav · nové SSH | 300/300 | 179,10 | 192,59 |
| Stav · sdílené SSH | 300/300 | 66,69 | 74,81 |
| Restart · HLCP | 30/30 | 165,31 | 191,24 |
| Restart · nové SSH | 30/30 | 272,37 | 301,17 |
| Restart · sdílené SSH | 30/30 | 159,41 | 199,46 |

Nejvyšší medián mělo u obou úloh nové SSH spojení. Sdílené SSH bylo v mediánu rychlejší než HLCP o 13,60 ms u stavu a o 5,90 ms u restartu. U všech 90 restartů nezávislé čtení potvrdilo stejný kontejner, nový čas startu a stav `running`. Uživatel čeká na návrat příkazu. Připravenost aplikace přijímat požadavky by vyžadovala vlastní sondu.

### Spotřeba při pravidelném dotazování

| Služba | CPU v klidu (%) | CPU při 1 dotazu/s (%) | Paměť při zátěži, medián každého běhu (MiB) |
|---|---:|---:|---:|
| Kontroler | 0,052–0,054 | 0,47–1,01 | 11,52 / 14,48 / 15,46 |
| Agent | 3,00–3,03 | 6,90–7,11 | 11,91 / 12,83 / 12,88 |

Všechny tři série dokončily 160 ze 160 naplánovaných dotazů. Procenta CPU jsou průměry za jednotlivá 120sekundová okna vůči jednomu logickému CPU. Paměť udává spotřebu účtovanou celé cgroup služby. Nejvyšší zaznamenaný vzorek agenta při zátěži činil 13,42 MiB.

![Využití CPU na obou službách](data/final-agent-720dcad/figures/cpu.png)

### Obnova a pravidla přístupu

Po ukončení agenta kontroler zaznamenal stav offline za 8,26 ms v mediánu. Od opětovného spuštění do prvního úspěšného živého dotazu uplynulo 107,69 ms v mediánu. Při pozastavení procesu s otevřeným spojením nastal stav offline za 15,03 s v mediánu. Po obnovení jsme ve všech pěti případech ověřili novou relaci a živý stav. Doba do úspěšné odpovědi se pohybovala od 1,05 do 15,86 s. Pokusy následovaly krátce po sobě a prodleva mezi pokusy o připojení se mohla přenášet mezi relacemi.

TLS odmítlo spojení bez klientského certifikátu i s certifikátem od cizí autority. API vrátilo 401 při chybném tokenu, 422 při nepovoleném restartu a 409 při opětovném použití ID pro odlišný příkaz. Dohledání i zopakování původního požadavku vrátilo shodný výsledek. Všechny tři nezávislé série poskytly očekávané odpovědi.

## 5. Závěr a meze měření

HLCP zpřístupnilo povolené operace nad kontejnery dvou aplikací přes společné rozhraní. Správce může dohledat výsledek příkazu i po jeho dokončení. Na měřené dvojici VM zabral živý dotaz v mediánu 80,29 ms a restart 165,31 ms. Při jednom živém dotazu za sekundu využil agent přibližně 7 % jednoho logického CPU.

Výsledky latence platí pro jeden kontejner na jedné dvojici VM. Časy zotavení popisují lokální experiment přes loopback. Dvouminutová okna dávají podklad pro odhad spotřeby při dané zátěži. Pro dlouhodobý provoz nebo souběžné řízení více uzlů by bylo potřeba další měření. [Data a postup ověření](data/final-agent-720dcad/README.md)

## Zdroje

1. OpenBSD, [ssh_config(5)](https://man.openbsd.org/ssh_config).
2. E. Rescorla, [TLS 1.3, RFC 8446](https://www.rfc-editor.org/rfc/rfc8446.html), 2018.
3. C. Bormann a P. Hoffman, [CBOR, RFC 8949](https://www.rfc-editor.org/rfc/rfc8949.html), 2020.
4. Linux kernel documentation, [Control Group v2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html).
