# P05a — Imported TLS validation a agent setup plán

Implementováno 2026-10-14 jako interní knihovna internal/onboarding.
Není to ještě CLI setup/průvodce ani součást preview.4.

## Kontrakt vlastní PKI

NewImportedTLS přijímá bounded snapshoty cert-chain, privátního leaf klíče,
peer trust a issuer trust. Každý má nejvýše 1 MiB; vstupy se kopírují.
Obyčejné JSON/String/GoString reporty materiál redigují. Žádné CA private key
vstupy, argv/env secrets, key generation, síť nebo externí OpenSSL proces.
Získání snapshotů z bezpečných souborů/stdin bude odpovědnost navazujícího CLI.

Validate vyžaduje validní cert/key match, unencrypted PKCS8/EC/RSA PEM klíč,
leaf nikoli CA, čas včetně NotBefore/NotAfter, explicitní role EKU a:

- Agent: přesně jedna URI odpovídající explicitní node identity.
- Controller: zvolené DNS/IP kryté SAN a explicitní server-auth.

Strict cert PEM nepovolí trailing prose, private blocks, malformed certs nebo
více než 16 certifikátů. CA bundles musí obsahovat platné BasicConstraints CA
certifikáty se sign-capable usage, pokud je KeyUsage extension přítomná.
Veřejný klíč leafu nesmí být totožný s dodaným CA klíčem: import omylem
znovupoužitého CA key jako deployment key se odmítne.
CA reuse mimo explicitně dodané CA/cert-chain snapshoty zjistit nelze.

Issuer trust ověřuje náš leaf přes x509.Verify včetně chain/pathLen constraints;
intermediates dodá cert-chain. Peer trust slouží pro ověření protější strany.
Nezaměňují se a nemusí být stejná PKI: agent může mít client leaf od jedné CA
a důvěřovat server leafu jiné CA. Explicitní issuer trust je samostatný input,
žádné automatické system-trust fallback nebo důvěra podle souboru leafu.
Importer neovládá externí issuer a nezajišťuje renewal.
Metadata report obsahuje jen roli a leaf expiraci; raw parser/x509 errors se
nepřeposílají. Platnost se vyhodnocuje k caller clock snapshotu.

## Čerstvý generic agent

PlanAgent binduje generated paths k přesnému private setup adresáři a sestaví:

| Soubor | Obsah / použití |
|---|---|
| agent.yaml | Validovaná generic konfigurace; resources/capabilities prázdné |
| agent.crt | Importovaný client leaf a jeho cert-chain |
| agent.key | Privátní client leaf klíč |
| ca.crt | Runtime controller trust |
| issuer-ca.crt | Lokální evidence trust pro ověření vlastního leafu, ne runtime peer trust |

Všechny soubory mají mode 0600. Existující adresář vyžaduje mode 0700 a explicitní
registrovaný setup.Owner. Novější P05c podporuje také potvrzené vytvoření posledního
adresáře pod bezpečným existujícím rodičem, viz [directory kontrakt](25-setup-directory-provisioning.md).
Factory vrací privátní AgentSetup s Preview/Apply/Close. Node ID, controller host:port, server name,
agent version a journal path jsou explicitní. DNS/IP syntax se ověří, nikoli
jejich dostupnost. Journal musí být clean absolute mimo config directory,
aby nemohl kolidovat s konfigurací nebo transakčními oblastmi.

Preview nic nezapisuje. Apply vyžaduje potvrzení a používá P04b transakci včetně
stale checks, backup, rollback/recovery hranic. Nevytváří ani neotvírá journal,
nedělá admission, neinstaluje units, nemění účet a nezapíná služby/resources.
Repeat s totožnými vstupy je unchanged, a nezíská novou identitu/klíč.
Different existing valid config/TLS data je conflict, nikoli automatické replace;
invalid/expired existing file také neznamená absence a nebude přepsán.
Tento fresh core tedy není automatický update/rotation/migration helper.

## Evidence a zbývající P05 práce

Windows full test/vet a Linux full race/test/vet prošly. Linux native test
pokrývá preview bez writes, odmítnutí bez consent, potvrzený zápis pěti souborů,
repeat unchanged při retained backup, resources [], preservation existujícího
journalu a ruční YAML změny. PKI testy pokrývají oddělené trust, key mismatch,
identity/name/expiry/EKU/CA-leaf, malformed/oversized/trailing input a redakci.
Intermediate chain a pathLen restriction test prošel; ARM64 compile-only prošel,
nikoli live ARM ani booted VM acceptance.

Navazuje skutečné imported agent/controller CLI a TTY/noninteractive wizard,
bounded bezpečné čtení input files/stdin, directory provisioning s consent,
controller env/API policy/token/allowlist/CLI a explicitní service orchestrace.
Imported externí renewal a dnešní restart/LoadCredential snapshot behavior musí
zůstat dokumentované; P05/release checkpoint B tímto jádrem není dokončen.
