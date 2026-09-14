"""Run sambacc with runtime paths valid under strict confinement."""

import os


RUNTIME_OPTIONS = (
    ("lock directory", "/var/lib/samba/lock"),
    ("pid directory", "/var/lib/samba/run"),
    ("ncalrpc dir", "/var/lib/samba/ncalrpc"),
    ("winbindd socket directory", "/var/lib/samba/winbindd"),
)


def configure_loadparm(loadparm):
    """Apply runtime directories that Samba's registry backend cannot store."""
    for name, value in RUNTIME_OPTIONS:
        loadparm.set(name, value)


def ensure_runtime_dirs(root="/"):
    """Create Samba runtime state below the writable /var/lib/samba layout."""
    for relative_path in (
        "var/lib/samba",
        "var/lib/samba/private",
        "var/lib/samba/lock",
        "var/lib/samba/run",
        "var/lib/samba/ncalrpc",
        "var/lib/samba/winbindd",
    ):
        os.makedirs(os.path.join(root, relative_path), exist_ok=True)

    os.chmod(os.path.join(root, "var/lib/samba/winbindd"), 0o755)


def load_runtime_loadparm(param, smbconf=None):
    """Load Samba configuration after setting registry-incompatible paths."""
    loadparm = param.get_context()
    configure_loadparm(loadparm)
    if smbconf is None:
        loadparm.load_default()
    else:
        loadparm.load(smbconf)
    return loadparm


def run():
    """Invoke sambacc after replacing its passdb loader with a confined one."""
    from sambacc import passdb_loader
    from sambacc import paths
    from sambacc.commands.main import main

    # sambacc 0.9 otherwise creates /run/samba before importing the
    # registry. That path is not writable by a strict snap.
    paths.ensure_samba_dirs = ensure_runtime_dirs
    parent_loader = passdb_loader.PassDBLoader

    class MicroCephPassDBLoader(parent_loader):
        """Load the passdb after replacing registry-incompatible paths."""

        def __init__(self, smbconf=None):
            param, passdb = passdb_loader._samba_modules()
            loadparm = load_runtime_loadparm(param, smbconf)
            passdb.set_secrets_dir(loadparm.get("private dir"))
            self._pdb = passdb.PDB(loadparm.get("passdb backend"))
            self._passdb = passdb

    passdb_loader.PassDBLoader = MicroCephPassDBLoader
    main()
