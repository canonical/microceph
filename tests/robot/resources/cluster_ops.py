"""Robot Framework library: pure parsing for multi-node cluster test scenarios.

The do-and-assert test bodies that used to live here (Check Client Configs,
Test Service Migration) are suite-specific, so they now live in
multi-node-tests/multi_node_tests.robot. What remains is the *decision* the
service-migration test needs: a pure, unit-testable parser for `microceph
status` output, replacing the remote ``grep -F -A 1 ... | grep -qE ...`` pipeline
(the "fetch raw, decide in Python" rule).
"""

import re

# The migrated services lines, anchored exactly as the original
# ``grep -qE '^ {2}Services: ...$'`` decision was: two leading spaces, then the
# precise service list, to end of line.
_SRC_MIGRATED_RE = re.compile(r"^ {2}Services: osd$")
_DST_MIGRATED_RE = re.compile(r"^ {2}Services: mds, mgr, mon$")


def _service_block_matches(lines, node_key, services_re):
    """Mirror ``... | grep -F -A 1 <node_key> | grep -qE <services_re>``.

    ``grep -F -A 1`` prints every line containing the literal *node_key* plus the
    single line after it; the second grep is true if ANY of those lines matches
    *services_re*. So this returns True iff some line that is, or immediately
    follows, a *node_key*-containing line matches the (whole-line anchored)
    services regex. Substring match for *node_key* preserves grep -F semantics.
    """
    for i, line in enumerate(lines):
        if node_key in line:
            for candidate in lines[i:i + 2]:
                if services_re.search(candidate):
                    return True
    return False


def parse_migration_status(status_text, src, dst):
    """Return ``(src_migrated, dst_migrated)`` parsed from ``microceph status`` output.

    Pure replacement for the two
    ``microceph status | grep -F -A 1 <node> | grep -qE '<services>' && echo yes || echo no``
    pipelines. *src_migrated* is True when *src*'s services line is exactly
    ``  Services: osd`` (OSD-only after the migration); *dst_migrated* is True when
    *dst*'s services line is exactly ``  Services: mds, mgr, mon``. The matching
    (grep -F substring for the node key, whole-line anchors for the services line)
    reproduces the original shell decision exactly, but is unit-testable and needs
    no remote grep.
    """
    lines = status_text.splitlines()
    src_migrated = _service_block_matches(lines, src, _SRC_MIGRATED_RE)
    dst_migrated = _service_block_matches(lines, dst, _DST_MIGRATED_RE)
    return (src_migrated, dst_migrated)
