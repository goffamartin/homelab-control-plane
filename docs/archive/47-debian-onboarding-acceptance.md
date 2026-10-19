> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# P05y — Booted Debian onboarding acceptance runbook

Historical imported-PKI gate. Managed setup is now implemented; use
[Quick Start](49-managed-quick-start.md) and the expanded
[managed live gate](51-managed-live-acceptance.md) for the next candidate.

Stav: připravený postup a read-only collector; **live akceptace dosud neprovedená**.
Lokální shell/core/package testy nejsou booted systemd ani reboot evidence.
Preview.4 se nemění, žádný tag/release/publish ani homelab deploy není součástí.

## 1. Izolace a vstupy před první mutací

Použít dvě nové disposable Debian 13 amd64 VM: controller a script-only system
agent. Nedělat tento test na současném controlleru 192.168.0.15 ani na rootless
Podman agentu 192.168.0.20. Jejich config, certs, journal/ledger a historie se
nemigrují. Test má jinou identitu `acceptance-system-01`, jiný leaf key/cert a
vlastní test controller admission. Žádná reuse node identity ani journal reset.

Předem potvrdit test IP/DNS, ssh přístup, sudo, synchronizovaný čas a možnost
rebootu. VM musí mít PID1 systemd, nikoli obyčejný build container. Zaznamenat
baseline a mít snapshot/recovery point před instalací. Snapshot není náhrada
off-host zálohy skutečných produkčních dat; v těchto VM žádná produkční data nejsou.

Pro imported cestu připravit **testovací** PKI: serverAuth leaf/key se SAN skutečné
test controller IP/DNS; clientAuth leaf/key s URI SAN
`urn:hlcp:node:acceptance-system-01`; issuer trust pro každou leaf a peer trust.
Privátní CA key nikdy nepředávat setupu ani collector reportu. Vystavení certs
je samostatný krok, nikoli funkce collector skriptu; managed PKI teprve navazuje.
PEM vstupy mají soukromé root-owned úložiště a mode 0600, parent 0700.

## 2. Exact-SHA lokální candidate, ne release

Zvolit jeden ověřený plný commit SHA obsahující P05y, čistý checkout a pinned
Go 1.27.2. Na build počítači, nikoli pomocí neověřeného curl-to-root:

```text
git rev-parse HEAD
git status --short
go run ./tools/release -version v0.0.0-test.p05y -targets linux/amd64 -out dist/acceptance-p05y-a
go run ./tools/release -version v0.0.0-test.p05y -targets linux/amd64 -out dist/acceptance-p05y-b
```

Použít explicitně pinned Go executable, pokud běžné `go` není 1.27.2. Oba output
adresáře musí být nové; bez --allow-dirty a bez přepisování starého outputu.
Manifest musí uvádět přesný SHA bez -dirty. Porovnat SHA256SUMS obou builds;
identické artifacts jsou local reproducibility evidence, nikoli důkaz původu.
Přenést z vlastního důvěryhodného build počítače do test VM. Zkontrolovat
SHA256SUMS před instalací a shodu manifest/--version revision. SHA checksum sám
nenahrazuje trust/signature; signed distribution se řeší v P11.

Fresh VM controller: instalovat jen příslušný controller .deb; agent VM: agent .deb.
Test verze je nižší než preview, proto ji neinstalovat jako downgrade produkce.
Balíčky samy nevytvoří credentials ani nezapnou/startnou službu. Nahrát také
hlcp-deployment.tar.gz z totožného buildu a extrahovat jen potřebné members:

```sh
tar -xzf hlcp-deployment.tar.gz \
  deploy/debian/inspect-onboarding-host.sh \
  docs/47-debian-onboarding-acceptance.md
sh deploy/debian/inspect-onboarding-host.sh --help
```

Collector je read-only, fixed role, bez secrets/config contents/journal/logů,
network requests či service mutací. Tiskne observed UTC, boot ID, caller IDs,
package/binary version a vybrané unit properties. Výstup není automatické PASS.
Exit nonzero může znamenat missing package/unit/probe; skutečný not-found probe
po collection interpretovat s úspěšným CLI reportem, nikoli jen exit code collectoru.

## 3. Fresh controller a agent setup

Na controller VM v interaktivním TTY:

```sh
sudo /usr/bin/hlcp-controller setup --imported --interactive --admit acceptance-system-01
sudo /usr/bin/hlcp-controller activate --enable
sudo /usr/bin/hlcp-controller activate --enable --apply --yes
sh deploy/debian/inspect-onboarding-host.sh controller
```

Setupu zadat skutečný test listener/server name a PEM paths, ne produkční adresy.
Wizard zobrazí plán a vyžádá souhlas. Ověřit private config a enable/start report;
API zůstává loopback. Pokud kterýkoli krok selže, zastavit a prohlédnout příčinu.
Neopakovat mutaci naslepo a nepoužívat force/reset/nové tokeny jako opravu.

Na agent VM:

```sh
sudo /usr/bin/hlcp-agent setup --interactive --scope system
```

Zadat test node ID, controller endpoint/server name, /etc/hlcp/agent directory,
/var/lib/hlcp/command-journal.json a připravené leaf/key/trust paths.
Wizard vytváří fresh config bez resources. Pro tento konkrétní test explicitně
upravit pouze capabilities/resources v novém agent.yaml (ostatní hodnoty zachovat):

```yaml
capabilities: [resource.control, script]
resources:
  - id: host-check
    type: task
    adapter: script
    actions: [diagnose]
    commands:
      diagnose:
        executable: /usr/bin/uname
        argv: [--all]
        working_directory: /
        timeout: 10s
```

Jde o root-owned systémový executable a safe root workdir; žádný shell string,
Docker/Podman, home/tmp dependency ani credential path. Resource selection
wizard je pozdější etapa P09, tento fixture krok ji nepředstírá.

## 4. Preview → probe → start → enable

Na agent VM, před mutací:

```sh
sh deploy/debian/inspect-onboarding-host.sh agent
sudo /usr/bin/hlcp-agent probe-resources --start --enable --json
```

Požadovat inactive/disabled main unit, absent probe, správnou installed unit bez
drop-ins a read_only=true CLI preview. Preview nesmí vytvořit journal ani
StateDirectory, spustit probe či agenta nebo změnit enable. Porovnat s baseline.
Pak výslovně potvrdit:

```sh
sudo /usr/bin/hlcp-agent probe-resources --start --enable --json --apply --yes
sh deploy/debian/inspect-onboarding-host.sh agent
```

Požadovat initial result.completed=true, start.probe.completed=true,
start.started=true, start.enabled=true a žádné uncertain. Collector musí ukázat
active/running, enabled, hlcp User/Group, správný package fragment, žádné drop-ins
a absent transient probe. Samotný active systemd stav ještě nepotvrzuje mTLS.

Na test controlleru jako oprávněný operátor:

```sh
sudo /usr/bin/hlcp --api-token-file /etc/hlcp/controller/api.token -n
sudo /usr/bin/hlcp --api-token-file /etc/hlcp/controller/api.token -n acceptance-system-01 -r
sudo /usr/bin/hlcp --api-token-file /etc/hlcp/controller/api.token -n acceptance-system-01 -r host-check -a diagnose
```

Požadovat online správného node/version, jediný fixture resource a úspěšný
výsledek explicitně vyžádané diagnose akce. Zapsat command_id a výsledek; žádný
replay akce jako způsob opravy. Token obsah neposílat, ani hash privátního key/tokenu.

## 5. Reboot a negativní brány

Reboot je samostatná explicitní operace uživatele na **test agent VM**. Ověřit na
controlleru návrat online bez přihlášení k agentovi; až potom ssh a collector.
BootID musí být jiný, main unit active/enabled, node identity stejná. Journal
nesmí zmizet/resetovat se; původní command result zůstane dohledatelný podle
retention kontraktu. Reboot neprovede onboarding probes ani jejich starý receipt.

Na již active agentu probe/start požadavek musí být odmítnut bez restartu a bez
nového transient probe. Další negative cases (dependency change, failed probe,
cancel/collection uncertainty) provést pouze v samostatném resetovatelném fixture,
nikoli změnou fungujícího homelabu. Zaznamenat refusal/uncertain, skutečný stav a
žádný automatický retry. Povolení po rebootu neobchází runtime allowlist/expiry.

## 6. Evidence a uzavření brány

Zapsat exact source SHA, manifest/checksum původ, installed versions, Debian/
systemd/arch, role a datum; baseline/preview/report/postcheck, controller online/
inventory/action result a boot ID před/po. Sdílet jen metadata; žádné PEM, tokens,
config s tajemstvími či nezkontrolované runtime logs. Collector není secret scrubber
pro jiné soubory ani automatický sandbox attestation verdict.

Brána tohoto workflow je PASS až po všech výše uvedených pozorováních. Zvlášť
evidovat remaining P05 user profiles/upgrade/recovery acceptance dle hlavního
plánu. Script-only amd64 test neprokazuje live ARM64, Docker/rootless Podman,
managed PKI nebo konečný checkpoint B. Zatím všechny live položky: NOT RUN.
