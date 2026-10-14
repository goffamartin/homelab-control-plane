# P05o — Čerstvý controller balíček a imported setup

Stav: změna zdrojového balíčku a lokální Debian 13 amd64 akceptace. Preview.4
se nemění; testovací balíčky 0.0.0-test.* jsou pouze lokální dirty validation
artifacts, nikoli publikovaná verze nebo deploy na homelab VM.

## Opravená překážka

Balíček dříve ukládal controller.env, nodes.json a api-policy.json do `/etc`.
Průvodce je podle správného no-overwrite kontraktu nesměl přepsat: zvolený
management listener a formát canonical JSON se lišily od packaged defaults.
Čerstvá instalace proto nemohla plynule pokračovat potvrzeným setupem.

Nový balíček vytváří pouze privátní adresář `/etc/hlcp/controller` (0700).
Veřejné příklady jsou pod `/usr/share/hlcp-controller` (0644), bez secrets.
Live config files nejsou v payloadu ani conffiles. Maintainer scripts negenerují
credentials, nezapínají službu a neupravují existující deployment config.

## Výsledný postup s vlastní PKI

1. Nainstalovat ověřený nový `.deb`.
2. Připravit server leaf/key, issuer trust a agent peer trust; CA private key
   se do průvodce nepředává.
3. `hlcp-controller setup` se zvoleným listenerem, server name a PEM paths
   nejprve ukáže read-only preview. `--apply --yes` vytvoří devět privátních souborů,
   včetně nových operator/observer tokenů. Souhlas je explicitní.
4. Opakování stejných vstupů zachová tokens/admission/config. Odlišný existující
   config zůstává konfliktem, nikoli automatickou opravou.
5. Následuje samostatná explicitní aktivace a případné `configure-client`.

Managed PKI/join a jednorázový download helper jsou další etapy, ne vlastnost
tohoto imported setupu. Není nutné kopírovat public příklady do `/etc` před setupem.

## Upgrade a odstranění

Existující konfigurace, leaf keys, credentials a ledger nejsou automaticky
přepisované, rotované nebo migrované. Přechod ze starých balíčků s conffiles
zachová jejich bytes, ale dpkg dál eviduje tyto soubory jako obsolete conffiles.
`purge` je podle staré ownership evidence může odstranit. Nemanipulujeme tiše
s dpkg databází; před purge je nutná privátní záloha. Nová wizard-created config
z fresh install není dpkg-owned a přežije remove/purge, stejně jako unmanaged key,
ledger a system account. Purge není credential revocation ani secure erase.

Legacy imported PKI/config může stále potřebovat samostatnou plánovanou migraci
na wizard contract (například issuer-ca.crt); tento blok ji automaticky neprovádí.

## Ověření

- Portable package tests ověřují prázdný live namespace, example paths/modes,
  absenci conffiles, explicitní allowlist, deterministic ar/tar a no-overwrite output.
- `deploy/debian/test-controller-setup.sh`: skutečná instalace nového balíčku,
  izolovaná testovací PKI, preview bez writes/tokens, confirmed setup, privátní
  permissions, byte-identical repeat bez rotace, runtime check-config, žádný ledger,
  odmítnutí changed config bez přepsání.
- `test-controller-package.sh`: install/upgrade/downgrade/remove/purge,
  manual unit conflict, disabled startup, zachování config/key/ledger/account
  a systemd unit syntax. Dvě test verze obsahují stejnou zdrojovou implementaci;
  nejde o ověření historické wire/state kompatibility.
- `test-controller-legacy-conffiles.sh`: syntetický legacy conffile package,
  modified i unchanged config přežijí upgrade; purge odpovídá skutečné dpkg evidenci.
  Syntetická metadata nejsou historický binární rollback.
- Windows full test/vet, Linux full race/test/vet; sestavené Linux amd64/arm64
  artifacts a ARM64 package metadata, nikoli live ARM64 execution.

Testovací image `deploy/debian/package-test.Dockerfile` ověřuje Debian VERSION_ID=13
a instaluje systemd/adduser/OpenSSL pouze do lokálního jednorázového prostředí.
Scriptové testy vyžadují HLCP_PACKAGE_TEST=disposable-container, /.dockerenv a
absenci booted systemd. Zdroj i balíčky jsou read-only mounts; testy běží bez sítě.
Booted VM service start/reboot, system resource sandbox a celý P05/checkpoint B
zůstávají otevřené.
