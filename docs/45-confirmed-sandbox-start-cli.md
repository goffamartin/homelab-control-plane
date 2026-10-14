# P05w — Confirmed sandbox probe-and-start CLI workflow

Stav: implementováno ve zdrojích, zatím nepublikováno v preview.4 a neověřeno
v booted Debian systemd. VM beze změn. P05/checkpoint B a live acceptance jsou
stále otevřené. Původní `activate` se nemění a nepřijímá uložené probe proof.

## Uživatelský postup

```text
hlcp-agent probe-resources --help
sudo hlcp-agent probe-resources --start --json
sudo hlcp-agent probe-resources --start --json --apply --yes
```

Příklady vyžadují instalovanou binárku obsahující tento blok, nikoli preview.4.
`--start` bez `--apply --yes` je pouze read-only plán. Jeden z potvrzovacích
přepínačů bez druhého je chyba před host I/O. Help také nečte konfiguraci.
Bez `--start` zůstává P05t samotná metadata kontrola beze startu hlavního agenta.

Pevně: local system manager, generic hlcp-agent.service, /etc/hlcp/agent,
script-only resources, root a imported setup. Agent musí být inactive a probe
absent; žádné override scope/unit/config, enable, restart nebo reset-failed.
Root se kontroluje před čtením konfigurace. Host bez trusted start adapteru je
START_UNAVAILABLE před jakoukoli kontrolou či mutací.

## Potvrzený workflow a report

1. Read-only source/config/TLS/profile/account preflight a plán prvního probe.
2. Explicitně potvrzený první sandbox probe, waited exit a verified collection.
3. Process-local P05u readiness plán; nejvýše 2min stáří, žádný persisted receipt.
4. P05v Start provede druhý sandbox probe se shodným dependency digestem,
   poslední source/expiry kontrolu, fixed start a ověřený active postcheck.

Celá potvrzená fáze má 90s context, jednotlivé probe/start mají vlastní kratší
limity. Žádný automatický retry, oprava, restart nebo rollback. Při changed source,
dependency mismatch či neověřeném průběhu se nepokračuje k dalšímu kroku.
Podrobné sandbox/source/TOCTOU hranice platí dle [P05v](44-sandbox-verified-agent-start.md).

JSON navíc obsahuje `start_requested` a případný `start` výsledek s dokončením
druhého probe, ověřeným `started` a `uncertain`. `result` nadále popisuje první
probe. Selhání až po první úspěšné kontrole má `read_only=false`, i když se start
vůbec nedispatchoval. START_INCOMPLETE znamená nejistý composite průběh;
`started=false` není důkaz, že služba nebyla spuštěná. Zkontrolovat skutečný stav.
Stale handoff je hlášen samostatně a neztrácí již dokončený první probe.

Veřejný výstup neobsahuje digests, paths, argv/env, identities ani credentials.
Writer failure vrací chybu bez opakování; úspěšná mutace může předcházet chybě
zápisu reportu. Konzument nesmí opakovat workflow jen podle neúspěšného exit code.

Start otevře běžný agent runtime, journal a outbound mTLS spojení. Samotné
kontroly nespouští resource akce. Verified active není potvrzení registrace,
inventory, controller connection ani funkčnosti skriptů; to ověří live acceptance.
Stav enabled se nemění, takže automatický start po rebootu tímto příkazem nevzniká.

## Lokální evidence a navazující brány

- Preview/help/invalid consent bez mutací; main entrypoint help bez agent.Load/Run.
- Fake-host celý workflow: právě dva probes před jedním startem, unsupported
  adapter, root-before-config, stale handoff a initial/second/start failures.
- Redacted report, zachování částečného výsledku a writer failure bez replay.
- Windows full test/vet, Linux full race/test/vet a ARM64 compile-only.

Následuje explicit enable kontrakt a booted Debian sandbox/collection/start,
controller registration a reboot acceptance. Žádný live úspěch ani nový release
se z lokálních testů neodvozuje.
