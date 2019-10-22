import dnf
import subprocess


class GloutonPlugin(dnf.Plugin):
    name = "glouton"

    def __init__(self, base, cli):
        super(GloutonPlugin, self).__init__(base, cli)

    def transaction(self):
        subprocess.call(
            ["/usr/lib/glouton/glouton-hook-package-modified"],
            stderr=open('/dev/null', 'w'),
        )
