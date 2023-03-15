#!/usr/bin/env python3

"""
glouton_script is the helper script that will take care of one of
* install glouton
* start glouton
* upgrade glouton
* uninstall glouton
"""

import os
import pathlib
import json
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request
import urllib.parse


def main():
    commands = {
        "install": do_install,  # This is the default
        "start": do_start,
        "upgrade": do_upgrade,
        "uninstall": do_uninstall,
        "make-symlink": do_symlink,
    }

    cmd_name = sys.argv[1] if len(sys.argv) > 1 else "install"

    if cmd_name not in commands:
        print("Usage: ./glouton_install.py install|start|upgrade|uninstall")
        return 1

    if os.getuid() != 0:
        print("This script must be run as root")
        return 1

    path_info = initiliaze()
    cmd = commands[cmd_name]

    cmd(path_info)
    return 0


def initiliaze():
    """ return path to Glouton install location
    """
    script_path = pathlib.Path(__file__).resolve()
    extract_location = script_path.parent
    install_location = extract_location.parent
    self_version = extract_location.name
    current_path = install_location / "current"
    if current_path.exists():
        try:
            installed_version = (install_location / "current").resolve().name
        except FileNotFoundError:
            installed_version = ""
    else:
        installed_version = ""

    path_info = {
        "script_path": script_path,
        "extract_location": extract_location,
        "install_location": install_location,
        "self_version": self_version,
        "installed_version": installed_version,
    }

    return path_info


def do_install(path_info):
    rc_file = pathlib.Path("/usr/local/etc/rc.d/glouton")
    if rc_file.exists():
        upgrade_file = pathlib.Path("/var/lib/glouton/upgrade")
        try:
            upgrade_file.touch()
        except FileNotFoundError:
            pass

        print("+ service glouton stop")
        subprocess.run(["service", "glouton", "stop"])

    update_cfg_file(path_info)

    current_path = path_info["install_location"] / "current"
    if (
        not current_path.exists()
        or current_path.resolve() != path_info["extract_location"]
    ):
        if current_path.exists():
            current_path.unlink()
        print(f"+ ln -s {path_info['self_version']} {current_path}")
        current_path.symlink_to(path_info["self_version"])

    if (
        path_info["installed_version"] != ""
        and path_info["installed_version"] != path_info["self_version"]
    ):
        path_to_old = path_info["install_location"] / path_info["installed_version"]
        print(f"+ rm -fr {path_to_old}")
        shutil.rmtree(path_to_old, ignore_errors=True)

    install_truenas_init_script(path_info)

    do_start(path_info)


def _get_api_token():
    result = subprocess.run(
        [
            "midclt",
            "call",
            "api_key.query",
            '[["name", "=", "glouton-install-api-key"]]',
        ],
        capture_output=True,
        check=True,
    )
    data = json.loads(result.stdout)
    for row in data:
        if row["name"] == "glouton-install-api-key":
            _delete_api_token(row["id"])

    print('Create temporary "glouton-install-api-key" to talk with TrueNAS API')
    result = subprocess.run(
        ["midclt", "call", "api_key.create", '{"name": "glouton-install-api-key"}'],
        capture_output=True,
        check=True,
    )
    data = json.loads(result.stdout)

    return data["key"], data["id"]


def _delete_api_token(token_id):
    subprocess.run(
        ["midclt", "call", "api_key.delete", str(token_id)],
        check=True,
        capture_output=True,
    )


def install_truenas_init_script(path_info):
    token, token_id = _get_api_token()

    try:
        _add_start_command(path_info, token)
    finally:
        _delete_api_token(token_id)


def _add_start_command(path_info, token):
    commands = _do_request("/api/v2.0/initshutdownscript/", token)

    command = (
        f'"{path_info["install_location"] / "current" / "glouton_install.py"}" start'
    )

    for cmd in commands:
        if cmd["comment"] == "Start bleemeo-agent":
            if cmd["command"] != command:
                print('Remove old "Start bleemeo-agent" init command')
                _do_request(
                    f'/api/v2.0/initshutdownscript/id/{cmd["id"]}/',
                    token,
                    method="DELETE",
                )
            else:
                # The same command already exists, do nothing.
                return

    payload = {
        "command": command,
        "comment": "Start bleemeo-agent",
        "enabled": True,
        "timeout": 30,
        "type": "COMMAND",
        "when": "POSTINIT",
    }

    print(f"Add {command} to TrueNAS startup")
    _do_request("/api/v2.0/initshutdownscript/", token, json_data=payload)


def _do_request(path, token, json_data=None, method=None):
    if json_data is not None:
        data = json.dumps(json_data).encode("utf8")
        method = method or "POST"
        ct = "application/json"
    else:
        data = None
        method = method or "GET"
        ct = None

    req = urllib.request.Request(
        urllib.parse.urljoin("http://localhost", path),
        data=data,
        method=method,
    )

    req.add_header("Authorization", f"Bearer {token}")
    if ct is not None:
        req.add_header("Content-Type", ct)

    result = urllib.request.urlopen(req)
    return json.load(result)


def update_cfg_file(path_info):
    new_etc_dir = path_info["install_location"] / path_info["self_version"] / "etc"
    old_etc_dir = path_info["install_location"] / path_info["installed_version"] / "etc"
    target_etc_dir = path_info["install_location"] / "etc"

    for full_path in new_etc_dir.glob("**/*.conf"):
        file = full_path.relative_to(new_etc_dir)
        new_file = new_etc_dir / file
        target_file = target_etc_dir / file
        old_file = old_etc_dir / file

        if not target_file.exists():
            target_file.parent.mkdir(parents=True, exist_ok=True)
            print(f"+ cp -p {new_file} {target_file}")
            shutil.copy2(new_file, target_file)
        elif path_info["installed_version"] != "":
            old_content = old_file.read_text()
            new_content = new_file.read_text()
            current_content = target_file.read_text()
            if old_content == current_content:
                if current_content != new_content:
                    print(f"+ cp -p {new_file} {target_file}")
                    shutil.copy2(new_file, target_file)
            else:
                print(
                    f"I: {target_file} was modified, not taking new version. To see the diff, run:"
                )
                print(f"diff -u {target_file} {new_file}")
        else:
            print(
                f"I: {target_file} was modified, not taking new version (see {new_file})"
            )


def do_start(path_info):
    do_symlink(path_info)
    setup_cron(path_info)

    # Most of the following is what would be present in postinstall of a package manager.

    rc_file = pathlib.Path("/usr/local/etc/rc.d/glouton")
    src_rc_file = path_info["install_location"] / "current" / "glouton.init"
    if not rc_file.exists():
        print(f"+ ln -s {src_rc_file} {rc_file}")
        rc_file.symlink_to(src_rc_file)

    result = subprocess.run(["sysrc", "-n", "glouton_enable"], capture_output=True)
    if result.stdout.decode("utf-8").strip() != "YES":
        print("+ sysrc glouton_enable=YES")
        subprocess.run(["sysrc", "glouton_enable=YES"], check=True, capture_output=True)

    print("+ service glouton start")
    subprocess.run(["service", "glouton", "start"], check=True)


def _highest_parent(path):
    while str(path.parent) not in [".", "", "/"]:
        path = path.parent

    return path


def do_upgrade(path_info):
    upgrade_url_file = pathlib.Path("/etc/glouton/freebsd-update-url")
    if upgrade_url_file.exists():
        upgrade_url = upgrade_url_file.read_text()
    else:
        upgrade_url = "https://packages.bleemeo.com/bleemeo-agent/freebsd/glouton_latest_freebsd_amd64.tar.gz"

    first_member = True
    new_version = None

    with urllib.request.urlopen(upgrade_url) as resp:
        tar = tarfile.open(mode="r|gz", fileobj=resp)
        for member in tar:
            member_path = pathlib.Path(member.name)
            if first_member:
                new_version = str(_highest_parent(member_path))
                if new_version == path_info["installed_version"]:
                    print(f"Already at latest version {new_version}")
                    return

                print(f"Upgrading glouton to {new_version}")
                first_member = False

            tar.extract(member, path_info["install_location"])

    if new_version is None:
        return

    new_install = path_info["install_location"] / new_version / "glouton_install.py"
    print(f"+ {new_install}")
    os.execv(
        str(new_install),
        [str(new_install)],
    )


def do_uninstall(path_info):
    etc_dir = path_info["install_location"] / "etc"
    var_lib = path_info["install_location"] / "var-lib"
    current_link = path_info["install_location"] / "current"
    self_path = path_info["extract_location"]

    if path_info["self_version"] != path_info["installed_version"]:
        print(
            f"This version isn't currently active version. This script will only remove {self_path}"
        )
        print(
            'To stop and remove Glouton from the system use "service glouton uninstall"'
        )
        print("Press ctrl+c to abort or wait 5 seconds")
    else:
        print(
            f"This script will stop and remove Glouton and all its data from the install location {path_info['install_location']}"
        )
        print("Press ctrl+c to abort or wait 5 seconds")
    time.sleep(5)

    if path_info["self_version"] != path_info["installed_version"]:
        print(f"+ rm -fr {self_path}")
        shutil.rmtree(self_path, ignore_errors=True)
        return

    print("+ service glouton stop")
    subprocess.run(["service", "glouton", "stop"])
    print("+ sysrc -x glouton_enabled")
    subprocess.run(["sysrc", "-x", "glouton_enable"])
    print(
        "+ rm /etc/glouton /usr/local/etc/glouton /var/lib/glouton /usr/local/etc/rc.d/glouton"
    )
    subprocess.run(
        [
            "rm",
            "/etc/glouton",
            "/usr/local/etc/glouton",
            "/var/lib/glouton",
            "/usr/local/etc/rc.d/glouton",
        ]
    )

    token, token_id = _get_api_token()

    try:
        commands = _do_request("/api/v2.0/initshutdownscript/", token)
        for cmd in commands:
            if cmd["comment"] == "Start bleemeo-agent":
                print('Remove "Start bleemeo-agent" init command')
                _do_request(
                    f'/api/v2.0/initshutdownscript/id/{cmd["id"]}/',
                    token,
                    method="DELETE",
                )
    finally:
        _delete_api_token(token_id)

    print(f"+ rm -fr {etc_dir}")
    shutil.rmtree(etc_dir, ignore_errors=True)

    print(f"+ rm -fr {var_lib}")
    shutil.rmtree(var_lib, ignore_errors=True)

    print(f"+ rm {current_link}")
    try:
        current_link.unlink()
    except FileNotFoundError:
        pass

    print(f"+ rm -fr {self_path}")
    shutil.rmtree(self_path, ignore_errors=True)


def do_symlink(path_info):
    """Create system symlink for /etc and /var/lib that point to Glouton installation location"""
    etc = path_info["install_location"] / "etc"
    etc_confd = path_info["install_location"] / "etc" / "conf.d"
    var_lib = path_info["install_location"] / "var-lib"

    target_etc1 = pathlib.Path("/etc/glouton")
    target_etc2 = pathlib.Path("/usr/local/etc/glouton")
    target_var_lib = pathlib.Path("/var/lib/glouton")

    if not etc.is_dir():
        etc.mkdir()

    if not etc_confd.is_dir():
        etc_confd.mkdir()

    if not var_lib.is_dir():
        var_lib.mkdir()

    links = [
        # (src, dst)
        (etc, target_etc1),
        (etc, target_etc2),
        (var_lib, target_var_lib),
    ]

    for src, dst in links:
        if not dst.exists():
            print(f"+ ln -s {src} {dst}")
            dst.symlink_to(src)


def setup_cron(path_info):
    """Add auto-upgarde cron"""
    target_script = path_info["install_location"] / "current" / "cron_upgrade.sh"
    target_crond = pathlib.Path("/etc/cron.d/glouton-auto-upgrade")
    new_content = f'0 7 * * 1-5 root "{target_script}" > /dev/null 2>&1\n'
    if target_crond.exists() and target_crond.read_text() == new_content:
        return

    print(f"Adding cron for auto-upgrade: {target_crond}")
    target_crond.write_text(new_content)


if __name__ == "__main__":
    sys.exit(main())
