> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05u — One-shot sandbox readiness revalidation core

Stav: implementovaný lifecycle core ve zdrojích, zatím bez native start adapteru
a CLI orchestrace. `probe-resources` z P05t je beze změny: jeho úspěch neodemyká
system agent activation. Preview.4, VM a journal/ledger beze změn.
P05/checkpoint B a live Debian sandbox/reboot acceptance zůstávají otevřené.

## Proč opakovat kontrolu

Samotný minulý probe není trvalé oprávnění. Core přijímá pouze skutečný úspěšně
dokončený `SandboxProbe` v témže procesu, nikoli důkaz z JSON, externí host nebo
uživatelský timestamp. Úvodní i opakovaná kontrola používají stejný trusted host,
source snapshot, skutečnou service identitu a pevné executable/workdir páry.

1. Dokončený probe zaznamená čas úspěšného postchecku. Maximální stáří pro
   předání je 2 minuty; přesná hranice i záporný elapsed time znamenají refusal.
   Produkční `time.Now` uchovává monotonic time ve stejném procesu.
2. `PlanSandboxReadiness` je read-only. Ověří dokončení, nenulový dependency
   digest, nespotřebovaný handoff, root identitu a aktuální source snapshot.
3. `Apply` vyžaduje samostatný souhlas. Znovu ověří source/stáří a těsně před
   dispatch spotřebuje handoff. Spustí pouze nový pevný sandbox metadata probe.
4. Inspector exit, unit collection, source/request/identity binding a celý
   dependency digest musí odpovídat původnímu důkazu. Nový nenulový, ale jiný
   digest nestačí. Po běhu se znovu ověří source i původní časový limit.
5. Až potom se vytvoří privátní in-memory readiness proof. Změny po dispatch,
   neověřený průběh, cancellation nebo vypršení během kontroly jsou uncertain
   bez automatického opakování či rollbacku.

Read-only fáze má vlastní 3s limit; potvrzená revalidation nejvýše 30s context
se stávajícími užšími native launcher limity. Dlouhá práce limit stáří neprodlužuje.
Odmítnutí před dispatch není vydávané za provedenou mutaci. Společný mutex a
handoff marker brání použití přes kopie probe/plánu i dvě nezávislé readiness
instance. Po jednom dispatch už další instance nový probe nespustí.

Veřejný výstup uvádí pouze read-only plán, potřebu souhlasu, dependency recheck
a maximální stáří. Žádné cesty, argv/env, PEM, identity, timestamps ani digests.
Žádný soubor/registry receipt se nevytváří a restart procesu důkaz ztrácí.

## Co tento blok ještě nezaručuje

Revalidation není start agenta a privátní proof zatím nemá veřejného konzumenta.
Nativní aktivace resources stále vyžaduje původní readiness gate a neakceptuje
tento proof. Další blok musí spojit revalidation se startem ve stejném potvrzeném
workflow, řešit explicit enable, source změny v lifecycle a nejasný výsledek startu.

Metadata kontrola nemůže garantovat, že nikdo soubory nezmění po posledním
čtení. Časový limit není filesystem lock ani důkaz úspěšného provedení skriptu.
Budoucí adapter nesmí z proof udělat neomezený grant; musí znovu posoudit jeho
vazby/stáří těsně před startem a výslovně popsat TOCTOU hranice. Runtime allowlist,
sanitizace a provozní kontrola výsledků zůstávají nutné.

## Lokální evidence

- Úspěšné dokončení, consent a redacted JSON/String/GoString.
- Neprovedený/neúspěšný/zero-proof probe, expiry boundary, clock backwards,
  caller/source změny, aktivní agent a existující probe odmítnuté bez dispatch.
- Dependency změny včetně jiného nenulového digestu; exit/collection/binding
  mismatch, source postcheck, expiry během probe a cancellation => uncertain.
- Kopie a souběžné nezávislé plány sdílí jediný dispatch; JSON nemůže vydat proof.
- Windows full test/vet, Linux full race/test/vet, Linux ARM64 compile-only.

Jde o lokální core evidence, nikoli skutečnou booted systemd/VM akceptaci.
