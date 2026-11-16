# HLCP — řízení služeb v HomeLabu

Martin Goffa · ANI-COM · 16. listopadu 2026

[Implementace v1.1.0](https://github.com/goffamartin/homelab-control-plane/tree/v1.1.0) ·
[Měřicí skripty a data](https://github.com/goffamartin/homelab-control-plane/tree/codex/semester-evaluation/evaluation) ·
[Anglická verze](report.md)

## Abstrakt

HomeLab Control Plane (HLCP) umožňuje z jednoho místa zjišťovat stav služeb a
spouštět předem povolené operace. Cílem práce bylo změřit jeho režii proti
přímému SSH a ověřit chování při odpojení a nepovolených požadavcích. Na dvojici
virtuálních strojů dosáhl HLCP při dotazu na stav mediánu 80,02 ms. Nové SSH
spojení mělo medián 181,42 ms a sdílené SSH spojení 69,92 ms. Při pozastavení
agenta testovací kontroler zaznamenal výpadek přibližně po 15 sekundách.

## 1. Problém a výzkumná otázka

Správa více služeb přes SSH vyžaduje znalost hostitele, runtime a příslušného
příkazu. HLCP eviduje uzly, prostředky a povolené akce a zpřístupňuje je přes
jednotné rozhraní. Výzkumná otázka zněla: Jakou režii má provedení operace přes
HLCP proti přímému SSH a jak se HLCP zachová při výpadku spojení nebo požadavku
mimo povolená pravidla?

## 2. Návrh a implementace

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
a 1 906 MiB systémem viditelné paměti na každé VM. HLCP v1.1.0 běžel na obou
strojích, Podman 5.4.2 na agentovi. Všechny varianty pracovaly se stejným rootless
kontejnerem pod stejným účtem na agentovi. Čas se měřil monotónními hodinami na
kontroleru od spuštění příkazu do jeho návratu.

Použili jsme tři varianty: HLCP s navázaným spojením, SSH s novým spojením
a SSH přes existující master spojení [1]. Pořadí variant se v blocích
promíchalo se zaznamenaným seedem. Stav měl tři bloky po 100 měřeních na
variantu, restart tři bloky po deseti. U restartu nezávislý příkaz po měření
ověřil stav `running` a nový čas startu.

CPU a paměť jsme měřili po jedné sekundě v cgroup každé služby [4]. Klid
znamenal běžný provoz bez uživatelských příkazů, s aktivním sledováním stavu
prostředků. Pod zátěží běžel jeden dotaz na stav za sekundu. Pro každý režim
proběhly tři páry měření po 120 sekundách. Zotavení a pravidla přístupu se
ověřovaly na dočasné dvojici přes loopback. Podklady jsou v
[přehledu experimentů](data/results.md).

## 4. Výsledky

### Latence příkazů

| Úloha a varianta | Úspěšné pokusy | Medián (ms) | p95 (ms) |
|---|---:|---:|---:|
| Stav · HLCP | 300/300 | 80,02 | 101,09 |
| Stav · nové SSH | 300/300 | 181,42 | 201,84 |
| Stav · sdílené SSH | 300/300 | 69,92 | 97,13 |
| Restart · HLCP | 30/30 | 173,18 | 201,27 |
| Restart · nové SSH | 30/30 | 268,74 | 291,44 |
| Restart · sdílené SSH | 30/30 | 160,50 | 187,27 |

Sdílené SSH mělo nejnižší medián v každém bloku. HLCP měl oproti němu
medián vyšší o 10,10 ms u stavu a 12,68 ms u restartu. Oproti novému SSH
byl medián HLCP nižší o 101,40 ms u stavu a 95,56 ms u restartu. Tabulka
shrnuje celou cestu provedení příkazu včetně spuštění procesů a práce runtime.

[Graf latence stavu](figures/status-latency.png) · [Graf latence restartu](figures/restart-latency.png)

### Spotřeba služeb

CPU je v procentech jednoho logického CPU. Paměť uvádí mediány jednotlivých
běhů podle `memory.current`.

| Služba | CPU v klidu | CPU při zátěži | Paměť v klidu (MiB) | Paměť při zátěži (MiB) |
|---|---:|---:|---:|---:|
| Kontroler | 0,053–0,054 % | 1,245–1,939 % | 11,16–11,17 | 13,00–15,04 |
| Agent | 2,944–2,982 % | 36,927–37,070 % | 20,79–20,82 | 21,50 / 22,16 / 54,99 |

Všech 480 plánovaných dotazů uspělo. CPU agenta při zátěži stouplo o zhruba
34 procentních bodů a ve všech třech bězích se drželo kolem 37 %. Paměť ve
třetím běhu dosáhla 66,95 MiB. Účetnictví za celou službu zahrnuje i spuštěné
příkazy Podmanu. Dostupná data neumožňují vyšší paměť přiřadit konkrétnímu
procesu nebo kategorii paměti.

[Graf využití CPU](figures/service-cpu.png) · [Graf spotřeby paměti](figures/service-memory.png)

### Zotavení a pravidla přístupu

Při pěti řádných ukončeních agenta se stav `offline` objevil po 8,34 ms v
mediánu. Od nového spuštění do prvního úspěšného dotazu uplynulo 103,29 ms
v mediánu. Při pěti pozastaveních bez zavření socketu byl medián do
`offline` 15,028 s. Odpovídá třem zmeškaným heartbeatům při intervalu 5 s.
Od obnovení procesu do prvního úspěšného dotazu uplynulo 4,447 s v mediánu
(1,116–16,882 s). Prodleva rostla v navazujících pokusech. Chování odpovídá
backoffu, který se resetuje až po stabilní relaci.

[Graf času obnovení spojení](figures/silent-recovery.png)

Kontroler odmítl chybějící a nedůvěryhodný klientský certifikát. API vrátilo
401 pro neplatný token, 422 pro nepovolenou akci a 409 pro jiný příkaz se
stejným ID. Dohledání výsledku a opakované odeslání původního příkazu vrátily
shodný výsledek. Každý případ proběhl jednou. Odpovědi API samy o sobě
neobsahují nezávislé měření počtu spuštění adaptéru.

## 5. Závěr a omezení

Na zkoumané dvojici VM poskytl HLCP při obou úlohách nižší latenci než nové
SSH spojení. Proti sdílenému SSH měl vyšší medián o jednotky až nízké desítky
milisekund. Za cenu této režie poskytuje inventář, povolené akce a dohledatelné
výsledky. Při sekundovém dotazování přitom vzrostlo využití CPU služby agenta
z přibližně 3 % na 37 % jednoho logického CPU.

Výsledky latence vycházejí z jednoho kontejneru a jedné dvojice VM. Časy obnovy
pocházejí z loopbacku a závisí na konfiguraci heartbeatů, intervalu pozorování
a backoffu. Kontrola stavu po restartu měří návrat do `running`. Připravenost
skutečné aplikace by vyžadovala vlastní sondu. Funkční ověření pokrývá konkrétní
případy autentizace a autorizace.

## Zdroje

1. OpenBSD, [ssh_config(5)](https://man.openbsd.org/ssh_config).
2. E. Rescorla, [TLS 1.3, RFC 8446](https://www.rfc-editor.org/rfc/rfc8446.html), 2018.
3. C. Bormann a P. Hoffman, [CBOR, RFC 8949](https://www.rfc-editor.org/rfc/rfc8949.html), 2020.
4. Linux kernel documentation, [Control Group v2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html).
