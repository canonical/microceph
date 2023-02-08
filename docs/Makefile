# Minimal makefile for Sphinx documentation
#

# You can set these variables from the command line, and also
# from the environment for the first two.
SPHINXOPTS    ?=
SPHINXBUILD   ?= sphinx-build
SOURCEDIR     = .
BUILDDIR      = _build
VENV          = sphinxenv/bin/activate


# Put it first so that "make" without argument is like "make help".
help:
	@$(SPHINXBUILD) -M help "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) $(O)

install:
	@echo "... setting up virtualenv"
	python3 -m venv sphinxenv
	. $(VENV); pip install --upgrade -r requirements.txt

	@echo "\n" \
		"--------------------------------------------------------------- \n" \
		"* watch, build and serve the documentation: make run \n" \
		"* check spelling: make spelling \n" \
		"\n" \
		"enchant must be installed in order for pyenchant (and therefore \n" \
		"spelling checks) to work. \n" \
		"--------------------------------------------------------------- \n"
run:
	. $(VENV); sphinx-autobuild "$(SOURCEDIR)" "$(BUILDDIR)"

spelling:
	. $(VENV); $(SPHINXBUILD) -b spelling "$(SOURCEDIR)" "$(BUILDDIR)"

.PHONY: help Makefile

# Catch-all target: route all unknown targets to Sphinx using the new
# "make mode" option.  $(O) is meant as a shortcut for $(SPHINXOPTS).
%: Makefile
	. $(VENV); $(SPHINXBUILD) -M $@ "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) $(O)
