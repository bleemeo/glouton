import dnf
import subprocess


class BleemeoPlugin(dnf.Plugin):
    name = "bleemeo"

    def __init__(self, base, cli):
        super(BleemeoPlugin, self).__init__(base, cli)

    def transaction(self):
        subprocess.call(
            ["/usr/lib/bleemeo/bleemeo-hook-package-modified"],
            stderr=open('/dev/null', 'w'),
        )
