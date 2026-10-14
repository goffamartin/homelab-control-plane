# P05t — Native source-bound resource probe CLI

Stav: implementováno ve zdrojích, nepublikováno v preview.4. VM beze změn.
P05/checkpoint B a system resource activation gate zůstávají otevřené.
Skutečné spuštění/collection v booted systemd zatím není ověřené.

## Rozhraní

```text
hlcp-agent probe-resources --help
sudo hlcp-agent probe-resources --json
sudo hlcp-agent probe-resources --json --apply --yes
```

První dva příkazy nic nespouštějí. Help nečte host/config; preview jen ověřuje
zdroje a připravuje počet executable/workdir kontrol. Příklady platí až pro
instalovanou binárku obsahující tento blok, nikoli současný preview.4 balíček.
Žádná scope/unit/config/path override možnost. Pevně system hlcp-agent.service,
konfigurace /etc/hlcp/agent a probe hlcp-agent-resource-probe.service.

Podmínky: root, lokální booted Linux systemd, trusted installed hlcp-agent,
systemctl/systemd-run, nezměněná packaged unit bez drop-ins, validní imported
setup a certifikáty, registered hlcp service účet bez dalších skupin, bezpečná
state/journal metadata. Hlavní agent musí být inactive, probe nesmí existovat.
Aktivní/failed/transitional agent se automaticky nezastaví ani neresetuje.
Systémový profil podporuje v tomto probe pouze omezené script metadata; Docker,
Podman, systemd resources a prázdný resource seznam nejsou touto cestou akceptované.

## Source binding a bezpečnost

Zdrojový inspector sdílí s aktivací ověřování instalované binárky, unit šablony,
identity, config/TLS snapshotů a journal metadata. Activation wrapper však dál
vždy vyžaduje původní resource readiness a nepřijímá probe jako bypass.
Probe místo root inspekce script souborů vytváří pouze konfigurační binding.
Source fingerprint navíc zahrnuje systemd-run snapshot, hlcp account a skutečný
active/enabled stav hlavní služby. Privátní důkazy ani konfigurace se nevypisují.

CLI má bounded read-only preflight, ověří root před čtením konfigurace a porovná
skutečný config digest s plánem. Core a native adapter znovu ověří source před
dispatch; adapter porovná i přesné executable/workdir páry. Po exit/collection
se source zkontroluje znovu. Změna certifikátů, konfigurace, accountu či služby
znamená refusal nebo uncertain podle toho, zda již byl probe dispatchován.

`--apply --yes` je explicitní mutace: transient unit a případné vytvoření/chown
StateDirectory systemd. Inspektor jen čte script metadata v pevném sandboxu.
Neprovádí nakonfigurované akce, controller připojení, journal open/lock, TLS
credential loading ani agent start/enable. Ověřuje skutečné sandbox properties,
process exit a collection dle [P05s kontraktu](41-native-sandbox-launcher.md).
Neověřený průběh je uncertain bez retry/rollback; člověk musí zkontrolovat stav.
Úspěch není důkaz funkčnosti skriptu, runtime prostředí či budoucího startu.

Evidence je jen v paměti během příkazu. Neukládá se readiness receipt a úspěch
probe neodemkne `activate --scope system` s resources. Čerstvost/revalidation
pro následný start a ochrana před změnou závislostí jsou další samostatný blok.

## Lokální ověření a další brány

- CLI fake-host testy: help/invalid options bez host I/O, preview bez launch,
  dvojí consent, root-before-config refusal, config binding, active/existing unit,
  stale plan, uncertain run/postcheck, redacted output a writer failure.
- Linux native refusal na ne-root/ne-booted hostu; žádný test nespouští službu.
- Stávající config/TLS/unit/readiness a launcher/IPC lifecycle testy zůstávají.
- Windows full test/vet, Linux full race/test/vet a ARM64 compile-only.

Navazuje readiness evidence lifecycle pro aktivaci a explicitní booted Debian
VM sandbox/cleanup/reboot acceptance. Bez těchto bran nejde o dokončený
plug-and-play release ani potvrzený live deployment.
