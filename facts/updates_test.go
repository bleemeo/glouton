// Copyright 2015-2023 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package facts

import "testing"

func TestDecodeFile(t *testing.T) { //nolint:maintidx
	// cases could be produced by reading /var/lib/update-notifier/updates-available or
	// running LANGUAGE=fr /usr/lib/update-notifier/apt-check --human-readable
	cases := []struct {
		name            string
		in              string
		pendingUpdate   int
		pendingSecurity int
	}{
		{
			name:            "no_update_fr",
			in:              "\n0 paquet peut être mis à jour.\n0 mise à jour de sécurité.\n",
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name:            "update_fr",
			in:              "\n45 paquets peut être mis à jour.\n16 mises à jour de sécurité.\n",
			pendingUpdate:   45,
			pendingSecurity: 16,
		},
		{
			name:            "update_en",
			in:              "\n63 packages can be updated.\n0 updates are security updates.\n",
			pendingUpdate:   63,
			pendingSecurity: 0,
		},
		{
			name:            "no_update_en",
			in:              "\n0 updates can be applied immediately.\n",
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name:            "invalid_file",
			in:              "This file is invalid. It's not generated by update-notifier",
			pendingUpdate:   -1,
			pendingSecurity: -1,
		},
		{
			name: "Ubuntu2204_update1_en",
			in: `
9 updates can be applied immediately.
To see these additional updates run: apt list --upgradable
`,
			pendingUpdate:   9,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_no_update_en",
			in: `
Expanded Security Maintenance for Applications is not enabled.

0 updates can be applied immediately.

Enable ESM Apps to receive additional future security updates.
See https://ubuntu.com/esm or run: sudo pro status
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_no_update_fr",
			in: `
La maintenance de sécurité étendue pour Applications n'est pas activée.

0 mise à jour peut être appliquée immédiatement.

Enable ESM Apps to receive additional future security updates.
See https://ubuntu.com/esm or run: sudo pro status
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_no_update_de",
			in: `
Erweiterte Sicherheitswartung (ESM) für Applications ist  nicht aktiviert.

0 Aktualisierungen können sofort angewendet werden.

Aktivieren Sie ESM Apps, um zusätzliche zukünftige Sicherheitsupdates zu erhalten.
Siehe https://ubuntu.com/esm oder führen Sie aus: sudo pro status
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_no_update_es",
			in: `
El mantenimiento de seguridad expandido para Applications está desactivado

Se pueden aplicar 0 actualizaciones de forma inmediata.

Active ESM Apps para recibir futuras actualizaciones de seguridad adicionales.
Vea https://ubuntu.com/esm o ejecute «sudo pro status»
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_esm_en",
			in: `
Expanded Security Maintenance for Applications is not enabled.

0 updates can be applied immediately.

2 additional security updates can be applied with ESM Apps.
Learn more about enabling ESM Apps service at https://ubuntu.com/esm
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_esm_fr",
			in: `
La maintenance de sécurité étendue pour Applications n'est pas activée.

0 mise à jour peut être appliquée immédiatement.

2 additional security updates can be applied with ESM Apps.
Learn more about enabling ESM Apps service at https://ubuntu.com/esm
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_esm_de",
			in: `
Erweiterte Sicherheitswartung (ESM) für Applications ist  nicht aktiviert.

0 Aktualisierungen können sofort angewendet werden.

2 zusätzliche Sicherheitsupdates können mit ESM Apps angewendet werden.
Erfahren Sie mehr über die Aktivierung des ESM Apps-Dienstes at https://ubuntu.com/esm
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_esm_es",
			in: `
El mantenimiento de seguridad expandido para Applications está desactivado

Se pueden aplicar 0 actualizaciones de forma inmediata.

2 actualizaciones de seguridad adicionales se pueden aplicar con ESM Apps.
Aprenda más sobre cómo activar el servicio ESM Apps at https://ubuntu.com/esm
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_esm2_en",
			in: `
Expanded Security Maintenance for Applications is not enabled.

1 update can be applied immediately.
To see these additional updates run: apt list --upgradable

4 additional security updates can be applied with ESM Apps.
Learn more about enabling ESM Apps service at https://ubuntu.com/esm
`,
			pendingUpdate:   1,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_esm2_zh",
			in: `
扩展安全维护（ESM）Applications 未启用。

1 更新可以立即应用。
要查看这些附加更新，请运行：apt list --upgradable

4 个额外的安全更新可以通过 ESM Apps 来获取安装。
可通过以下途径了解如何启用 ESM Apps：at https://ubuntu.com/esm
`,
			pendingUpdate:   1,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2204_esm3_en",
			in: `
Expanded Security Maintenance for Applications is not enabled.

5 updates can be applied immediately.
5 of these updates are standard security updates.
To see these additional updates run: apt list --upgradable

7 additional security updates can be applied with ESM Apps.
Learn more about enabling ESM Apps service at https://ubuntu.com/esm
`,
			pendingUpdate:   5,
			pendingSecurity: 5,
		},
		{
			name: "Ubuntu2204_esm3_zh",
			in: `
扩展安全维护（ESM）Applications 未启用。

5 更新可以立即应用。
这些更新中有 5 个是标准安全更新。
要查看这些附加更新，请运行：apt list --upgradable

7 个额外的安全更新可以通过 ESM Apps 来获取安装。
可通过以下途径了解如何启用 ESM Apps：at https://ubuntu.com/esm
`,
			pendingUpdate:   5,
			pendingSecurity: 5,
		},
		{
			name: "Ubuntu2204_update2_en",
			in: `
Expanded Security Maintenance for Applications is not enabled.

4 updates can be applied immediately.
4 of these updates are standard security updates.
To see these additional updates run: apt list --upgradable

Enable ESM Apps to receive additional future security updates.
See https://ubuntu.com/esm or run: sudo pro status
`,
			pendingUpdate:   4,
			pendingSecurity: 4,
		},
		{
			name: "Ubuntu2204_update2_fr",
			in: `
La maintenance de sécurité étendue pour Applications n'est pas activée.

4 mises à jour peuvent être appliquées immédiatement.
4 de ces mises à jour sont des mises à jour de sécurité.
Pour afficher ces mises à jour supplémentaires, exécuter : apt list --upgradable

Enable ESM Apps to receive additional future security updates.
See https://ubuntu.com/esm or run: sudo pro status
`,
			pendingUpdate:   4,
			pendingSecurity: 4,
		},
		{
			name: "Ubuntu2204_update2_de",
			in: `
Erweiterte Sicherheitswartung (ESM) für Applications ist  nicht aktiviert.

4 Aktualisierungen können sofort angewendet werden.
4 dieser Aktualisierungen sind Standard-Sicherheitsaktualisierungen.
Zum Anzeigen dieser zusätzlichen Aktualisierungen bitte »apt list --upgradable« ausführen

Aktivieren Sie ESM Apps, um zusätzliche zukünftige Sicherheitsupdates zu erhalten.
Siehe https://ubuntu.com/esm oder führen Sie aus: sudo pro status
`,
			pendingUpdate:   4,
			pendingSecurity: 4,
		},
		{
			name: "Ubuntu2204_update2_es",
			in: `
El mantenimiento de seguridad expandido para Applications está desactivado

Se pueden aplicar 4 actualizaciones de forma inmediata.
4 de estas son actualizaciones de seguridad estándares.
Para ver estas actualizaciones adicionales, ejecute: apt list --upgradable

Active ESM Apps para recibir futuras actualizaciones de seguridad adicionales.
Vea https://ubuntu.com/esm o ejecute «sudo pro status»
`,
			pendingUpdate:   4,
			pendingSecurity: 4,
		},
		{
			name: "Ubuntu1804_update1_en",
			in: `
Expanded Security Maintenance for Applications is not enabled.

7 updates can be applied immediately.
6 of these updates are standard security updates.
To see these additional updates run: apt list --upgradable

12 additional security updates can be applied with ESM Apps.
Learn more about enabling ESM Apps service at https://ubuntu.com/esm
`,
			pendingUpdate:   7,
			pendingSecurity: 6,
		},
		{
			name: "Ubuntu1804_update1_fr",
			in: `
La maintenance de sécurité étendue pour Applications n'est pas activée.

7 mises à jour peuvent être appliquées immédiatement.
6 de ces mises à jour sont des mises à jour de sécurité.
Pour afficher ces mises à jour supplémentaires, exécuter : apt list --upgradable

12 mises à jour de sécurité supplémentaires peuvent être appliquées avec ESM Apps.
En savoir plus sur l'activation du service ESM Apps at https://ubuntu.com/esm
`,
			pendingUpdate:   7,
			pendingSecurity: 6,
		},
		{
			name: "Ubuntu1804_update1_de",
			in: `
Erweiterte Sicherheitswartung (ESM) für Applications ist  nicht aktiviert.

7 Aktualisierungen können sofort angewendet werden.
6 dieser Aktualisierungen sind Standard-Sicherheitsaktualisierungen.
Zum Anzeigen dieser zusätzlichen Aktualisierungen bitte »apt list --upgradable« ausführen

12 zusätzliche Sicherheitsupdates können mit ESM Apps angewendet werden.
Erfahren Sie mehr über die Aktivierung des ESM Apps-Dienstes at https://ubuntu.com/esm
`,
			pendingUpdate:   7,
			pendingSecurity: 6,
		},
		{
			name: "Ubuntu1804_update1_es",
			in: `
El mantenimiento de seguridad expandido para Applications está desactivado

Se pueden aplicar 7 actualizaciones de forma inmediata.
6 de estas son actualizaciones de seguridad estándares.
Para ver estas actualizaciones adicionales, ejecute: apt list --upgradable

12 actualizaciones de seguridad adicionales se pueden aplicar con ESM Apps.
Aprenda más sobre cómo activar el servicio ESM Apps at https://ubuntu.com/esm
`,
			pendingUpdate:   7,
			pendingSecurity: 6,
		},
		{
			name: "Ubuntu2004_update",
			in: `
77 updates can be applied immediately.
To see these additional updates run: apt list --upgradable
`,
			pendingUpdate:   77,
			pendingSecurity: 0,
		},
		{
			name: "Ubuntu2004_update2",
			in: `
112 updates can be installed immediately.
5 of these updates are security updates.
To see these additional updates run: apt list --upgradable
`,
			pendingUpdate:   112,
			pendingSecurity: 5,
		},
		{
			name: "Ubuntu2210_update1_en",
			in: `
9 updates can be applied immediately.
8 of these updates are standard security updates.
To see these additional updates run: apt list --upgradable	
`,
			pendingUpdate:   9,
			pendingSecurity: 8,
		},
		{
			name: "Ubuntu2210_update1_fr",
			in: `
9 mises à jour peuvent être appliquées immédiatement.
8 de ces mises à jour sont des mises à jour de sécurité.
Pour afficher ces mises à jour supplémentaires, exécuter : apt list --upgradable	
`,
			pendingUpdate:   9,
			pendingSecurity: 8,
		},
		{
			name: "Ubuntu2210_update1_de",
			in: `
9 Aktualisierungen können sofort angewendet werden.
8 dieser Aktualisierungen sind Standard-Sicherheitsaktualisierungen.
Zum Anzeigen dieser zusätzlichen Aktualisierungen bitte »apt list --upgradable« ausführen	
`,
			pendingUpdate:   9,
			pendingSecurity: 8,
		},
		{
			name: "Ubuntu2210_update1_es",
			in: `
Se pueden aplicar 9 actualizaciones de forma inmediata.
8 de estas son actualizaciones de seguridad estándares.
Para ver estas actualizaciones adicionales, ejecute: apt list --upgradable	
`,
			pendingUpdate:   9,
			pendingSecurity: 8,
		},
		{
			name:            "Ubuntu2210_no_update_en",
			in:              `0 updates can be applied immediately.`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name:            "Ubuntu2210_no_update_fr",
			in:              `0 mise à jour peut être appliquée immédiatement.`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name:            "Ubuntu2210_no_update_de",
			in:              `0 Aktualisierungen können sofort angewendet werden.`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			name:            "Ubuntu2210_no_update_es",
			in:              `Se pueden aplicar 0 actualizaciones de forma inmediata.`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		// The next 5 entries are crafter after reading source code / executing part of the code,
		// so they may be inaccurate or might be impossible to generate.
		{
			name:            "hand_made_update1",
			in:              "\n3 updates can be installed immediately.\n",
			pendingUpdate:   3,
			pendingSecurity: 0,
		},
		{
			name:            "hand_made_update2",
			in:              "\n1 update can be applied immediately.\n",
			pendingUpdate:   1,
			pendingSecurity: 0,
		},
		{
			name:            "hand_made_update3",
			in:              "\n5 updates can be applied immediately.\n",
			pendingUpdate:   5,
			pendingSecurity: 0,
		},
		{
			name: "hand_made_esm1",
			in: ("UA Apps: Extended Security Maintenance (ESM) is enabled.\n" +
				"\n" +
				"5 updates can be applied immediately.\n" +
				"2 of these updates are UA Apps: ESM security updates.\n" +
				"To see these additional updates run: apt list --upgradable\n"),
			pendingUpdate:   5,
			pendingSecurity: 0,
		},
		{
			name: "hand_made_esm2",
			in: ("UA Apps: Extended Security Maintenance (ESM) is not enabled.\n" +
				"\n" +
				"5 updates can be applied immediately.\n" +
				"To see these additional updates run: apt list --upgradable\n" +
				"\n" +
				"2 additional security updates can be applied with UA Apps: ESM\n" +
				"Learn more about enabling UA Apps: ESM service at https://ubuntu.com/esm),\n"),
			pendingUpdate:   5,
			pendingSecurity: 0,
		},
	}
	for _, c := range cases {
		c := c

		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			gotUpdate, gotSecurity := decodeUpdateNotifierFile([]byte(c.in))
			if gotUpdate != c.pendingUpdate || gotSecurity != c.pendingSecurity {
				t.Errorf("decodeUpdateNotifierFile == %d, %d want %d, %d", gotUpdate, gotSecurity, c.pendingUpdate, c.pendingSecurity)
			}
		})
	}
}

func TestDecodeAPTCheck(t *testing.T) {
	cases := []struct {
		in              string
		pendingUpdate   int
		pendingSecurity int
	}{
		{
			in:              "0;0",
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			in:              "45;16",
			pendingUpdate:   45,
			pendingSecurity: 16,
		},
		{
			in:              "63;0",
			pendingUpdate:   63,
			pendingSecurity: 0,
		},
	}
	for i, c := range cases {
		gotUpdate, gotSecurity := decodeAPTCheck([]byte(c.in))
		if gotUpdate != c.pendingUpdate || gotSecurity != c.pendingSecurity {
			t.Errorf("decodeAPTCheck([case %d]) == %d, %d want %d, %d", i, gotUpdate, gotSecurity, c.pendingUpdate, c.pendingSecurity)
		}
	}
}

func TestDecodeAPTGet(t *testing.T) {
	cases := []struct {
		in              string
		pendingUpdate   int
		pendingSecurity int
	}{
		{
			in: `NOTE: This is only a simulation!
      apt-get needs root privileges for real execution.
      Keep also in mind that locking is deactivated,
      so don't depend on the relevance to the real current situation!
`,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			in: `NOTE: This is only a simulation!
      apt-get needs root privileges for real execution.
      Keep also in mind that locking is deactivated,
      so don't depend on the relevance to the real current situation!
Inst bsdutils [1:2.27.1-6ubuntu3.7] (1:2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf bsdutils (1:2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst util-linux [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf util-linux (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst mount [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf mount (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst uuid-runtime [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst psmisc [22.21-2.1build1] (22.21-2.1ubuntu0.1 Ubuntu:16.04/xenial-updates [amd64])
Inst slapd [2.4.42+dfsg-2ubuntu3.6] (2.4.42+dfsg-2ubuntu3.7 Ubuntu:16.04/xenial-updates [amd64]) []
Inst libldap-2.4-2 [2.4.42+dfsg-2ubuntu3.6] (2.4.42+dfsg-2ubuntu3.7 Ubuntu:16.04/xenial-updates [amd64])
Inst apache2 [2.4.18-2ubuntu3.10] (2.4.18-2ubuntu3.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64]) []
Inst apache2-bin [2.4.18-2ubuntu3.10] (2.4.18-2ubuntu3.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64]) []
Inst apache2-utils [2.4.18-2ubuntu3.10] (2.4.18-2ubuntu3.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64]) []
Inst apache2-data [2.4.18-2ubuntu3.10] (2.4.18-2ubuntu3.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [all])
Inst uuid-dev [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64]) []
Inst libuuid1 [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf libuuid1 (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst libblkid1 [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf libblkid1 (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst libfdisk1 [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf libfdisk1 (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst libmount1 [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf libmount1 (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst libsmartcols1 [2.27.1-6ubuntu3.7] (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf libsmartcols1 (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Inst libisc-export160 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Inst libdns-export162 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Inst bind9 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst dnsutils [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst bind9-host [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst libisc160 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst libisccc140 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst libisccfg140 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst liblwres141 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst libdns162 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst libirs141 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst bind9utils [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64]) []
Inst libbind9-140 [1:9.10.3.dfsg.P4-8ubuntu1.14] (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Inst btrfs-tools [4.4-1ubuntu1] (4.4-1ubuntu1.1 Ubuntu:16.04/xenial-updates [amd64])
Inst docker-ce-cli [5:19.03.1~3-0~ubuntu-xenial] (5:19.03.2~3-0~ubuntu-xenial Docker CE:xenial [amd64])
Inst docker-ce [5:19.03.1~3-0~ubuntu-xenial] (5:19.03.2~3-0~ubuntu-xenial Docker CE:xenial [amd64])
Inst dovecot-imapd [1:2.2.22-1ubuntu2.10] (1:2.2.22-1ubuntu2.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64]) []
Inst dovecot-core [1:2.2.22-1ubuntu2.10] (1:2.2.22-1ubuntu2.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Inst libcups2 [2.1.3-4ubuntu0.9] (2.1.3-4ubuntu0.10 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Inst linux-modules-4.4.0-161-generic (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Inst linux-image-4.4.0-161-generic (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Inst linux-modules-extra-4.4.0-161-generic (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Inst linux-generic [4.4.0.159.167] (4.4.0.161.169 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64]) []
Inst linux-image-generic [4.4.0.159.167] (4.4.0.161.169 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64]) []
Inst linux-headers-4.4.0-161 (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [all]) []
Inst linux-headers-4.4.0-161-generic (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64]) []
Inst linux-headers-generic [4.4.0.159.167] (4.4.0.161.169 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Inst linux-libc-dev [4.4.0-159.187] (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Inst snapd [2.39.2ubuntu0.2] (2.40 Ubuntu:16.04/xenial-updates [amd64])
Conf uuid-runtime (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf psmisc (22.21-2.1ubuntu0.1 Ubuntu:16.04/xenial-updates [amd64])
Conf libldap-2.4-2 (2.4.42+dfsg-2ubuntu3.7 Ubuntu:16.04/xenial-updates [amd64])
Conf slapd (2.4.42+dfsg-2ubuntu3.7 Ubuntu:16.04/xenial-updates [amd64])
Conf apache2-bin (2.4.18-2ubuntu3.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf apache2-utils (2.4.18-2ubuntu3.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf apache2-data (2.4.18-2ubuntu3.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [all])
Conf apache2 (2.4.18-2ubuntu3.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf uuid-dev (2.27.1-6ubuntu3.8 Ubuntu:16.04/xenial-updates [amd64])
Conf libisc-export160 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf libdns-export162 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf libisc160 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf libdns162 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf libisccc140 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf libisccfg140 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf libbind9-140 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf libirs141 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf liblwres141 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf bind9utils (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf bind9 (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf bind9-host (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf dnsutils (1:9.10.3.dfsg.P4-8ubuntu1.15 Ubuntu:16.04/xenial-updates [amd64])
Conf btrfs-tools (4.4-1ubuntu1.1 Ubuntu:16.04/xenial-updates [amd64])
Conf docker-ce-cli (5:19.03.2~3-0~ubuntu-xenial Docker CE:xenial [amd64])
Conf docker-ce (5:19.03.2~3-0~ubuntu-xenial Docker CE:xenial [amd64])
Conf dovecot-core (1:2.2.22-1ubuntu2.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf dovecot-imapd (1:2.2.22-1ubuntu2.12 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf libcups2 (2.1.3-4ubuntu0.10 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf linux-modules-4.4.0-161-generic (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf linux-image-4.4.0-161-generic (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf linux-modules-extra-4.4.0-161-generic (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf linux-image-generic (4.4.0.161.169 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf linux-headers-4.4.0-161 (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [all])
Conf linux-headers-4.4.0-161-generic (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf linux-headers-generic (4.4.0.161.169 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf linux-generic (4.4.0.161.169 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf linux-libc-dev (4.4.0-161.189 Ubuntu:16.04/xenial-updates, Ubuntu:16.04/xenial-security [amd64])
Conf snapd (2.40 Ubuntu:16.04/xenial-updates [amd64])
	  		`,
			pendingUpdate:   46,
			pendingSecurity: 16,
		},
	}
	for i, c := range cases {
		gotUpdate, gotSecurity := decodeAPTGet([]byte(c.in))
		if gotUpdate != c.pendingUpdate || gotSecurity != c.pendingSecurity {
			t.Errorf("decodeAPTGet([case %d]) == %d, %d want %d, %d", i, gotUpdate, gotSecurity, c.pendingUpdate, c.pendingSecurity)
		}
	}
}

func TestDecodeDNF(t *testing.T) {
	cases := []struct {
		in              string
		pendingUpdate   int
		pendingSecurity int
	}{
		{
			in:              ``,
			pendingUpdate:   0,
			pendingSecurity: 0,
		},
		{
			in: `FEDORA-2019-ae52a20ff6 bugfix         curl-7.65.3-3.fc30.x86_64
FEDORA-2019-40235845dc bugfix         dnf-4.2.8-1.fc30.noarch
FEDORA-2019-40235845dc bugfix         dnf-data-4.2.8-1.fc30.noarch
FEDORA-2019-40235845dc bugfix         dnf-yum-4.2.8-1.fc30.noarch
FEDORA-2019-080ded7584 enhancement    elfutils-default-yama-scope-0.177-1.fc30.noarch
FEDORA-2019-080ded7584 enhancement    elfutils-libelf-0.177-1.fc30.x86_64
FEDORA-2019-080ded7584 enhancement    elfutils-libs-0.177-1.fc30.x86_64
FEDORA-2019-83fa1cfd0f bugfix         file-libs-5.36-4.fc30.x86_64
FEDORA-2019-bd9727c538 bugfix         glib2-2.60.7-1.fc30.x86_64
FEDORA-2019-2e9a65b50a bugfix         glibc-2.29-22.fc30.x86_64
FEDORA-2019-2e9a65b50a bugfix         glibc-common-2.29-22.fc30.x86_64
FEDORA-2019-2e9a65b50a bugfix         glibc-minimal-langpack-2.29-22.fc30.x86_64
FEDORA-2019-e11df00d17 unknown        kmod-libs-26-3.fc30.x86_64
FEDORA-2019-ae52a20ff6 bugfix         libcurl-7.65.3-3.fc30.x86_64
FEDORA-2019-40235845dc bugfix         libdnf-0.35.2-1.fc30.x86_64
FEDORA-2019-ee3442fd65 enhancement    libevent-2.1.8-7.fc30.x86_64
FEDORA-2019-c187cb7e12 bugfix         libgcc-9.2.1-1.fc30.x86_64
FEDORA-2019-1f05925d82 Low/Sec.       libgcrypt-1.8.5-1.fc30.x86_64
FEDORA-2019-c187cb7e12 bugfix         libstdc++-9.2.1-1.fc30.x86_64
FEDORA-2019-b7da9ab2c4 enhancement    libxcrypt-4.4.8-1.fc30.x86_64
FEDORA-2019-b7da9ab2c4 enhancement    libxcrypt-compat-4.4.8-1.fc30.x86_64
FEDORA-2019-6a7f921663 enhancement    mkpasswd-5.5.1-1.fc30.x86_64
FEDORA-2019-b364562f30 bugfix         openssl-1:1.1.1c-6.fc30.x86_64
FEDORA-2019-b364562f30 bugfix         openssl-libs-1:1.1.1c-6.fc30.x86_64
FEDORA-2019-f0f0ff64bb bugfix         pcre2-10.33-13.fc30.x86_64
FEDORA-2019-40235845dc bugfix         python3-dnf-4.2.8-1.fc30.noarch
FEDORA-2019-40235845dc bugfix         python3-hawkey-0.35.2-1.fc30.x86_64
FEDORA-2019-40235845dc bugfix         python3-libdnf-0.35.2-1.fc30.x86_64
FEDORA-2019-e800418aac bugfix         python3-rpm-4.14.2.1-5.fc30.x86_64
FEDORA-2019-e800418aac bugfix         rpm-4.14.2.1-5.fc30.x86_64
FEDORA-2019-e800418aac bugfix         rpm-build-libs-4.14.2.1-5.fc30.x86_64
FEDORA-2019-e800418aac bugfix         rpm-libs-4.14.2.1-5.fc30.x86_64
FEDORA-2019-e800418aac bugfix         rpm-plugin-systemd-inhibit-4.14.2.1-5.fc30.x86_64
FEDORA-2019-e800418aac bugfix         rpm-sign-libs-4.14.2.1-5.fc30.x86_64
FEDORA-2019-24e1d561e5 Important/Sec. systemd-241-12.git1e19bcd.fc30.x86_64
FEDORA-2019-24e1d561e5 Important/Sec. systemd-libs-241-12.git1e19bcd.fc30.x86_64
FEDORA-2019-24e1d561e5 Important/Sec. systemd-pam-241-12.git1e19bcd.fc30.x86_64
FEDORA-2019-24e1d561e5 Important/Sec. systemd-rpm-macros-241-12.git1e19bcd.fc30.noarch
FEDORA-2019-e574f1bcad bugfix         vim-minimal-2:8.1.1912-1.fc30.x86_64
FEDORA-2019-6a7f921663 enhancement    whois-nls-5.5.1-1.fc30.noarch`,
			pendingUpdate:   40,
			pendingSecurity: 5,
		},
	}
	for i, c := range cases {
		gotUpdate, gotSecurity := decodeDNF([]byte(c.in))
		if gotUpdate != c.pendingUpdate || gotSecurity != c.pendingSecurity {
			t.Errorf("decodeAPTGet([case %d]) == %d, %d want %d, %d", i, gotUpdate, gotSecurity, c.pendingUpdate, c.pendingSecurity)
		}
	}
}

func TestDecodeYUM(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{
			in:   ``,
			want: 0,
		},
		{
			in: `Updated Packages
bind-libs-lite.x86_64                32:9.9.4-74.el7_6.2                      updates           
bind-license.noarch                  32:9.9.4-74.el7_6.2                      updates           
bleemeo-agent.noarch                 19.08.05.145316-1.el7                    bleemeo-agent-repo
bleemeo-agent-telegraf.noarch        19.08.05.145316-1.el7                    bleemeo-agent-repo
container-selinux.noarch             2:2.107-1.el7_6                          extras            
containers-common.x86_64             1:0.1.37-1.el7.centos                    extras            
curl.x86_64                          7.29.0-51.el7_6.3                        updates           
device-mapper.x86_64                 7:1.02.149-10.el7_6.8                    updates           
device-mapper-event.x86_64           7:1.02.149-10.el7_6.8                    updates           
device-mapper-event-libs.x86_64      7:1.02.149-10.el7_6.8                    updates           
device-mapper-libs.x86_64            7:1.02.149-10.el7_6.8                    updates           
docker.x86_64                        2:1.13.1-102.git7f2769b.el7.centos       extras            
docker-client.x86_64                 2:1.13.1-102.git7f2769b.el7.centos       extras            
docker-common.x86_64                 2:1.13.1-102.git7f2769b.el7.centos       extras            
glib2.x86_64                         2.56.1-4.el7_6                           updates           
glibc.x86_64                         2.17-260.el7_6.6                         updates           
glibc-common.x86_64                  2.17-260.el7_6.6                         updates           
kernel.x86_64                        3.10.0-957.27.2.el7                      updates           
kernel-tools.x86_64                  3.10.0-957.27.2.el7                      updates           
kernel-tools-libs.x86_64             3.10.0-957.27.2.el7                      updates           
kexec-tools.x86_64                   2.0.15-21.el7_6.4                        updates           
libcurl.x86_64                       7.29.0-51.el7_6.3                        updates           
libssh2.x86_64                       1.4.3-12.el7_6.3                         updates           
libteam.x86_64                       1.27-6.el7_6.1                           updates           
lvm2.x86_64                          7:2.02.180-10.el7_6.8                    updates           
lvm2-libs.x86_64                     7:2.02.180-10.el7_6.8                    updates           
microcode_ctl.x86_64                 2:2.1-47.5.el7_6                         updates           
oci-systemd-hook.x86_64              1:0.2.0-1.git05e6923.el7_6               extras            
oci-umount.x86_64                    2:2.5-1.el7_6                            extras            
python.x86_64                        2.7.5-80.el7_6                           updates           
python-libs.x86_64                   2.7.5-80.el7_6                           updates           
python-perf.x86_64                   3.10.0-957.27.2.el7                      updates           
python36-PyYAML.x86_64               3.12-1.el7                               epel              
python36-chardet.noarch              3.0.4-1.el7                              epel              
selinux-policy.noarch                3.13.1-229.el7_6.15                      updates           
selinux-policy-targeted.noarch       3.13.1-229.el7_6.15                      updates           
systemd.x86_64                       219-62.el7_6.9                           updates           
systemd-libs.x86_64                  219-62.el7_6.9                           updates           
systemd-sysv.x86_64                  219-62.el7_6.9                           updates           
teamd.x86_64                         1.27-6.el7_6.1                           updates           
tuned.noarch                         2.10.0-6.el7_6.4                         updates           
tzdata.noarch                        2019b-1.el7                              updates           
vim-minimal.x86_64                   2:7.4.160-6.el7_6                        updates           `,
			want: 43,
		},
		{
			in: `Updated Packages
bind-libs-lite.x86_64          32:9.9.4-74.el7_6.2   updates
bind-license.noarch            32:9.9.4-74.el7_6.2   updates
bleemeo-agent.noarch           19.08.05.145316-1.el7 bleemeo-agent-repo
bleemeo-agent-telegraf.noarch  19.08.05.145316-1.el7 bleemeo-agent-repo
container-selinux.noarch       2:2.107-1.el7_6       extras 
containers-common.x86_64       1:0.1.37-1.el7.centos extras 
curl.x86_64                    7.29.0-51.el7_6.3     updates
device-mapper.x86_64           7:1.02.149-10.el7_6.8 updates
device-mapper-event.x86_64     7:1.02.149-10.el7_6.8 updates
device-mapper-event-libs.x86_64
                               7:1.02.149-10.el7_6.8 updates
device-mapper-libs.x86_64      7:1.02.149-10.el7_6.8 updates
docker.x86_64                  2:1.13.1-102.git7f2769b.el7.centos
                                                     extras 
`,
			want: 12,
		},
	}
	for i, c := range cases {
		got := decodeYUMOne([]byte(c.in))
		if got != c.want {
			t.Errorf("decodeAPTGet([case %d]) == %d want %d", i, got, c.want)
		}
	}
}