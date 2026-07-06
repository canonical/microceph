import datetime
import os
import textwrap
import yaml

# Configuration for the Sphinx documentation builder.
# All configuration specific to our project should be done in this file.
#
# A complete list of built-in Sphinx configuration values:
# https://www.sphinx-doc.org/en/master/usage/configuration.html
#
# The Sphinx Stack uses the Canonical Sphinx theme to keep all documentation consistent
# and on brand:
# https://github.com/canonical/canonical-sphinx

#######################
# Project information #
#######################

# Project name
project = "MicroCeph"

# Author name; used in the default copyright statement in the page footer
author = "Canonical Ltd."

# The year in the copyright statement
copyright = f"{datetime.date.today().year}"

# Sidebar documentation title
# To disable the title, set it to an empty string.
html_title = project + " documentation"

# Documentation website URL
ogp_site_url = os.environ.get("READTHEDOCS_CANONICAL_URL", "/")

# Preview name of the documentation website
# TODO: To use a different name for the project in previews, update the next line.
ogp_site_name = project

# Preview image URL
# TODO: To customise the preview image, update the next line.
ogp_image = "https://assets.ubuntu.com/v1/cc828679-docs_illustration.svg"

# Product favicon; shown in bookmarks, browser tabs, etc.
# TODO: To customise the favicon, uncomment and update the next line.
# html_favicon = "_static/favicon.png"

# Dictionary of values to pass into the Sphinx context for all pages:
# https://www.sphinx-doc.org/en/master/usage/configuration.html#confval-html_context
html_context = {
    # Product page URL; can be different from product docs URL
    # TODO: Change to your product website URL, dropping the 'https://' prefix (e.g.,
    #       'ubuntu.com/lxd'). If there's no such website, remove the {{ product_page }}
    #       link from the _templates/header.html file.
    "product_page": "canonical.com/ceph",

    # Product tag image; the orange part of your logo, shown in the page header
    # TODO: To add a tag image, uncomment and update as needed.
    # 'product_tag': '_static/tag.png',
    
    # Your Discourse instance URL
    # TODO: Change to your Discourse instance URL or leave empty.
    "discourse": "",

    # Your Mattermost channel URL
    # TODO: Change to your Mattermost channel URL or leave empty.
    "mattermost": "https://chat.canonical.com/canonical/channels/ceph",

    # Your Matrix channel URL
    # TODO: Change to your Matrix channel URL or leave empty.
    "matrix": "https://matrix.to/#/#ceph-general:ubuntu.com",

    # Your documentation GitHub repository URL If set, links for viewing the
    # documentation source files and creating GitHub issues are added at the bottom of
    # each page.
    # TODO: Change to your documentation GitHub repository URL or leave empty.
    "github_url": "https://github.com/canonical/microceph",

    # Docs branch in the repo; used in links for viewing the source files
    "repo_default_branch": "main",

    # Docs location in the repo; used in links for viewing the source files
    "repo_folder": "/docs/",

    # TODO: To enable or disable the Previous / Next buttons at the bottom of pages
    # Valid options: none, prev, next, both
    "sequential_nav": "both",

    # TODO: To enable listing contributors on individual pages, set to True
    "display_contributors": True,

    # Required for feedback button
    "github_issues": "enabled",

    # Passes the top-level 'author' value to the theme
    "author": author,

    # Documentation license information
    "license": {
        # TODO: Specify your project's license.
        # For the name, we recommend using the standard shorthand identifier from
        # https://spdx.org/licenses
        "name": "AGPL-3.0",
        # TODO: Link directly to your project's license statement.
        "url": "https://github.com/canonical/microceph/blob/main/COPYING",
    },
}

# TODO: To enable the edit button on pages, uncomment and change the link to a
# public repository on GitHub or Launchpad. Any of the following link domains
# are accepted:
# - https://github.com/canonical/microceph"
# - https://launchpad.net/example
# - https://git.launchpad.net/example
html_theme_options = {
    'source_edit_link': 'https://github.com/canonical/microceph',
}

# Project slug
# TODO: If your documentation is hosted on https://documentation.ubuntu.com/,
#       uncomment and set to the RTD slug.
# slug = ''

###############################################################
# Sitemap configuration: https://sphinx-sitemap.readthedocs.io/
###############################################################

# Use RTD canonical URL to ensure duplicate pages have a specific canonical URL
html_baseurl = os.environ.get("READTHEDOCS_CANONICAL_URL", "/")

# sphinx-sitemap uses html_baseurl to generate the full URL for each page:
sitemap_url_scheme = "{link}"

# Include `lastmod` dates in the sitemap:
sitemap_show_lastmod = True

# URL scheme. Add language and version scheme elements
# When configured with RTD variables, check for RTD environment so manual runs succeed:
if 'READTHEDOCS_VERSION' in os.environ:
    version = os.environ["READTHEDOCS_VERSION"]
    sitemap_url_scheme = '{version}{link}'
else:
    sitemap_url_scheme = 'MANUAL/{link}'


# TODO: Exclude pages that aren't user-facing from the sitemap (e.g., module pages
# generated by autodoc).
# Pages excluded from the sitemap:
sitemap_excludes = [
    "404/",
    "genindex/",
    "search/",
]

################################
# Template and asset locations #
################################

html_static_path = ["_static"]
templates_path = ["_templates"]

#############
# Redirects #
#############

# Add redirects to the 'redirects.txt' file
# https://sphinxext-rediraffe.readthedocs.io/en/latest/

# To set up redirects in the Read the Docs project dashboard:
# https://docs.readthedocs.io/en/stable/guides/redirects.html

rediraffe_redirects = "redirects.txt"

# Strips '/index.html' from destination URLs when building with 'dirhtml'
rediraffe_dir_only = True

############################
# sphinx-llm configuration #
############################

# This description is included in llms.txt to provide some initial context for your
# product docs.
# TODO: Add a description in the form "This is the documentation for <product name>,
# <first sentence of home page>".
llms_txt_description = textwrap.dedent(
    """\
    This is the official documentation for MicroCeph, an opinionated orchestration tool for Ceph clusters at all scales, and the easiest way to get up and running with Ceph.
    """
)

###########################
# Link checker exceptions #
###########################

# A regex list of URLs that are ignored by 'make linkcheck'
# Remove or adjust the ACME entry after you update the contributing guide
linkcheck_ignore = [
    "http://127.0.0.1:8000",
    "https://github.com/canonical/ACME/*",
    "https://github.com",
    "https://matrix\.to/.*",
    "https://example.com",
    "https://tracker.ceph.com/.*",
]

# A regex list of URLs where anchors are ignored by 'make linkcheck
linkcheck_anchors_ignore_for_url = [
    r"https://github\.com/.*",
    r"https://matrix.to/*",
    # SourceForge domains often block linkcheck
    r"https://.*\.sourceforge\.(net|io)/.*",
    ]

# How long the link checker will wait for a response for each request
# TODO: Decrease to improve run time or increase if links frequently time out.
# External documentation sites (canonical.com, ubuntu.com, ...) intermittently
# return 429 (rate limiting), 502 (bad gateway), or read timeouts while the
# link checker is running. These are transient infra failures, not broken
# links, yet they fail the docs build. Raise the retry count, the rate-limit
# back-off window, and the per-request timeout so transient errors clear
# instead of failing the build.
# See https://github.com/canonical/microceph/issues/717
linkcheck_timeout = 60
linkcheck_retries = 5
linkcheck_rate_limit_timeout = 600

########################
# Configuration extras #
########################

# Custom MyST syntax extensions; see
# https://myst-parser.readthedocs.io/en/latest/syntax/optional.html
# NOTE: By default, the following MyST extensions are enabled:
#   - substitution
#   - deflist
#   - linkify
# myst_enable_extensions = set()

# Custom Sphinx extensions; see
# https://www.sphinx-doc.org/en/master/usage/extensions/index.html

# NOTE: The canonical_sphinx extension is required for the Shinx Stack.
#       It automatically enables the following extensions:
#       - custom-rst-roles
#       - myst_parser
#       - notfound.extension
#       - related-links
#       - sphinx_copybutton
#       - sphinx_design
#       - sphinx_reredirects
#       - sphinx_tabs.tabs
#       - sphinxcontrib.jquery
#       - sphinxext.opengraph
#       - terminal-output
#       - youtube-links
extensions = [
    "canonical_sphinx",
    "notfound.extension",
    "sphinx_design",
    "sphinx_rerediraffe",
    "sphinx_reredirects",
    "sphinx_tabs.tabs",
    "sphinxcontrib.jquery",
    "sphinxext.opengraph",
    "sphinx_config_options",
    "sphinx_contributor_listing",
    "sphinx_filtered_toctree",
    "sphinx_llm.txt",
    "sphinx_related_links",
    "sphinx_roles",
    "sphinx_terminal",
    "sphinx_ubuntu_images",
    "sphinx_youtube_links",
    "sphinxcontrib.cairosvgconverter",
    "sphinx_last_updated_by_git",
    "sphinx.ext.intersphinx",
    "sphinx_sitemap",
]

# Excludes files or directories from processing
exclude_patterns = [
    "doc-cheat-sheet*",
    ".venv*",
]

# Adds custom CSS files, located remotely or in 'html_static_path'.
html_css_files = ["https://assets.ubuntu.com/v1/d86746ef-cookie_banner.css"]

# Adds custom JavaScript files, located under 'html_static_path'

html_js_files = ["https://assets.ubuntu.com/v1/287a5e8f-bundle.js"]


# Appends extra markup to the end of every document written in reST
# rst_epilog = """
# """

# Feedback button at the top; enabled by default
# TODO: Disable the button if your project is unsuitable for public feedback.
# disable_feedback_button = True

# Your manpage URL
# TODO: To enable manpage links, uncomment and replace {codename} with required
#       release, preferably an LTS release (e.g. noble). Do *not* substitute
#       {section} or {page}; these will be replaced by sphinx at build time
#
# NOTE: If set, adding ':manpage:' to an .rst file
#       adds a link to the corresponding man section at the bottom of the page.
# manpages_url = 'https://manpages.ubuntu.com/manpages/{codename}/en/' + \
#     'man{section}/{page}.{section}.html'

# Specifies a reST snippet to be prepended to each .rst file
# This defines a :center: role that centers table cell content.
# This defines a :h2: role that styles content for use with PDF generation.
rst_prolog = """
.. role:: center
   :class: align-center
.. role:: h2
    :class: hclass2
.. role:: woke-ignore
    :class: woke-ignore
.. role:: vale-ignore
    :class: vale-ignore
"""

# Workaround for https://github.com/canonical/canonical-sphinx/issues/34

if "discourse_prefix" not in html_context and "discourse" in html_context:
    html_context["discourse_prefix"] = html_context["discourse"] + "/t/"

# Workaround for substitutions.yaml

if os.path.exists('./reuse/substitutions.yaml'):
    with open('./reuse/substitutions.yaml', 'r') as fd:
        myst_substitutions = yaml.safe_load(fd.read())

# Disable automatic fallback resolution so that all intersphinx references
# must be written explicitly as :external+key:ref:`label`.
# This prevents ambiguous or accidental cross-project resolution.
intersphinx_disabled_reftypes = ['*']

# Intersphinx mapping: links to external Sphinx documentation sets
# Keys are short identifiers used in :external+key:ref:`label` roles.
# The second tuple element (None) means Sphinx fetches objects.inv automatically
# from the base URL.

# Configuration for Intersphinx projects

intersphinx_mapping = {
    # Upstream Ceph documentation
    'upstream-ceph': ('https://docs.ceph.com/en/latest/', None),
    # Snap packaging documentation
    'snapcraft': ('https://snapcraft.io/docs/', None),
    # MicroCloud documentation
    'microcloud': ('https://documentation.ubuntu.com/microcloud/latest/', None),
    # Juju documentation (relevant for the MicroCeph charm)
    'juju': ('https://documentation.ubuntu.com/juju/latest/', None),
    # Ubuntu release notes
    'ubuntu-release-notes': ('https://documentation.ubuntu.com/release-notes/', None),
}
