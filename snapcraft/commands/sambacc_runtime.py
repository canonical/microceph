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


def ctdb_nodes_with_reserved_slots(nodes):
    """Render missing/retired PNNs as comments, never ignorable blank lines.

    Sambacc 0.9 fills absent ranks with empty strings. CTDB skips those lines
    and renumbers later nodes. Use the same renderer for initial lists and the
    long-lived metadata monitor so a reload cannot reintroduce that problem.
    """
    entries = {}
    for entry in nodes:
        rank = entry["pnn"]
        if type(rank) is not int or rank < 0 or rank in entries:
            raise ValueError("invalid or duplicate CTDB PNN")
        entries[rank] = entry
    result = ["#"] * (max(entries, default=-1) + 1)
    for rank, entry in entries.items():
        if entry["state"] in ("gone", "changed"):
            continue
        address = entry["node"]
        if not isinstance(address, str) or not address.strip():
            raise ValueError("missing CTDB node address")
        result[rank] = address
    return result


def run():
    """Invoke sambacc after replacing its passdb loader with a confined one."""
    from sambacc import ctdb
    from sambacc import passdb_loader
    from sambacc import paths
    from sambacc.commands.main import main

    # sambacc 0.9 otherwise creates /run/samba before importing the
    # registry. That path is not writable by a strict snap.
    paths.ensure_samba_dirs = ensure_runtime_dirs
    ctdb._cluster_meta_to_ctdb_nodes = ctdb_nodes_with_reserved_slots
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
