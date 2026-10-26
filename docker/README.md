# Retired Docker tooling

The experimental Ubuntu 14.04 blocks, assembler, generated init script, and fig recipes have been retired. They installed and configured Git globally and used an obsolete source build and shell deployment layout.

There is no supported container build in this tree. Run the native executable directly with `gogs web --config <absolute config path>`, matching runtime assets and an installed supported Mercurial executable. Task 15 supplies the Go archive builder and operator instructions; these removed recipes are not a deployment prerequisite.
