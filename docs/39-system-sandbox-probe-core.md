# P05q — Potvrzovaný lifecycle systémového sandbox probe

Stav: implementované a testované interní jádro, **bez native launcheru, inspektoru
a CLI napojení**. Žádná testovací jednotka se v této etapě nespouští.
System resource activation gate zůstává zavřená; preview.4 a VM se nemění.

## Kontrakt a rozsah první verze

`PlanSandboxProbe` je oddělený od read-only activation preview. Pouze čte source
evidence; `Apply` vyžaduje samostatný souhlas, protože budoucí launcher vytvoří
transient jednotku/namespace. Žádný implicitní probe při setup/activate/doctor.

První core podporuje jen metadata allow-listed script executables/workdirs ve
fixed system profilu. Docker/Podman/systemd resources se odmítají před host
inspection. Je to zúžení tohoto nového probe, nikoli změna obecného agent runtime.
Textově hidden paths /home, /root, /run/user a host /tmp, /var/tmp se také odmítnou.
Read-only workdir může být vhodný pro metadata kontrolu; není to záruka zápisu
nebo funkčnosti skriptu. Symlink targets a interpreter teprve ověří native inspector.

Maxima: 256 resources, 128 configured script checks a 1 MiB canonical request
binding. Opakované executable/workdir páry se deterministicky deduplikují.
Inspektor dostane jen immutable dvojice cest a opaque request bindings, nikoli
resource/action IDs, argv/environment, journal paths, PEM nebo arbitrary units.
Plný config včetně argv/env se váže do fingerprintu, nikoli do probe payloadu.

## Evidence a potvrzené provedení

Read-only host snapshot musí v budoucnu doložit:

- Skutečný root provisioning caller a registrovaný non-root service UID/GID.
- Installed binary/manager/profile, validated imported config/TLS a state metadata
  v source fingerprintu; journal content a lock se neotevírají.
- Canonical config fingerprint odpovídající přesně konfiguraci daného plánu.
- Neaktivního hlavního agenta a neexistující fixed probe jednotku.

Apply před dispatch znovu ověří caller a identický snapshot. Kopie plánu sdílejí
mutex/single-use stav. Před voláním RunProbe se plán spotřebuje; ani error,
cancellation či partial cleanup neopravňuje automatický retry/rollback.

RunProbe smí spustit jen installed fixed metadata inspector v odpovídajícím
sandboxu účtu `hlcp`, nikdy konfigurovaný skript/runtime ani main agent start/enable.
Launcher nesmí přidávat groups/grants, oslabovat sandbox, provádět resource akce
nebo číst/zamykat journal. Fixed unit je `hlcp-agent-resource-probe.service`.
Konflikt s existující jednotkou se odmítá, nikoli stop/reset/overwrite.

Evidence z trusted local host adaptéru musí potvrdit source/request bindings,
skutečný UID/GID, nenulový dependency fingerprint, ukončený inspector a úklid
transient unit. Poté musí read-only source snapshot stále odpovídat. Nejde o
cryptographic attestation od nedůvěryhodného klienta; native adapter tyto vlastnosti
musí skutečně ověřit. Fake-host test je nesmí vydávat za VM důkaz.

Planning inspection má 3s deadline, Apply host operation celkem 30s. Host musí
honorovat context/cancellation; samotný Go context nezastaví nekooperující host.
Budoucí native launcher potřebuje bounded process I/O, timeout/kill/reap,
bezpečný credential/input transport a explicitní cleanup evidence.

Public output obsahuje jen fixed unit, počet kontrol, read_only/consent a
completed/uncertain. Paths, config/env/argv, private host errors a fingerprints
se nezveřejňují. Před dispatch nevalidní/stale plán nic nespustí. Po dispatch
neověřený exit/cleanup/source znamená uncertain, ne tvrzení, že se nic nestalo.

## Testy a navazující brána

Core testy pokrývají consent, root/service identities, inactive/absent unit gates,
config binding, resource/path/size limits, dedup/order, redakci, defensive input
copy, stale source před dispatch, source/request/identity/result mismatches,
zero dependency proof, exit/cleanup failure, cancellation, post-check failure,
nejistý výsledek bez retry, copy-plan single-use a concurrent Apply pod race testy.
Windows full test/vet, Linux full race/test/vet a Linux ARM64 compile-only jsou
lokální evidence; nikoli inspektor, systemd sandbox, VM či ARM64 execution acceptance.

Další blok: fixed inspector + native bounded transport/launcher a opt-in CLI.
Receipt/evidence zatím není persistovaná ani přijímaná pro activation; nejprve
musí být vyřešená její čerstvost/source binding a revalidation před startem.
Booted Debian sandbox/reboot akceptace a celý P05/checkpoint B zůstávají otevřené.
