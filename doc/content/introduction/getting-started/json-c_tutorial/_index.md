---
title: "JSON-C tutorial"
date: 2019-01-17T15:26:15Z
draft: false
weight: 4
---

## Step 0: Making sure your software builds

This tutorial will use the JSON-C library as the project
under analysis. Let's retrievethe JSON-C source code first,
in a specific revision that we know works with this tutorial:

	> cd doc/content/introduction/getting-started/json-c_tutorial
	> git clone https://github.com/json-c/json-c.git
	...
	> cd json-c
	> git reset --hard bf29aa0f
	...
	> cd ..

All JSON-C specific parts of the build have been automated in a [build
script](build.sh). Let's run it to make sure our
environment is configured to build JSON-C from scratch:

	> ./build.sh
	...

If this works, wonderful. If not, please dig into the output and check
for errors. Quartermaster tracks what sources are compiled and what
targets are linked in your project. To be sure that everything gets
compiled again after instrumentation, let's clean the repository:

	> cd json-c
	> git clean -fxd
	...
	> cd ..

## Step 1: Start a Quartermaster master process

Every build that is instrumented with Quartermaster needs a master
process. Every Quartermaster master needs a configuration file,
usually called [qmstr.yaml](qmstr.yaml). The configuration
file is located in the tutorial/ directory because in our case, we are
avoiding to make changes to the project under analysis. If you want
to learn how to fill in the configuration file, visit the [qmstr.yaml instructions](../../../configuration/_index.md).
Let's start the master:

	> eval `qmstrctl start --wait --config qmstr.yaml`
	> qmstrctl create package:json-c --version someversion

The `wait` flag makes sure that the command returns only after the
master has finished starting up and is fully operational. The `config`
flag points to the configuration file. If it is not specified,
`qmstrctl` looks for a file called `qmstr.yaml` in  the current
directory.

## Step 2: Build (this time under instrumentation)

For C based builds, Quartermaster modifies the environment in a way
that it can trace the calls to the compiler and linker. This
instrumentation is build-system specific. The details of how build
system instrumentation works are beyond this tutorial. Thankfully,
Quartermaster comes with a tool that automates the necessary tweaks to
the shell environment. It modifies the environment for the command it
executes, and then resets it again before exiting. In the next step,
we will use this tool to call the build script from step 0 (above):

	> qmstr run ./build.sh
	...

This script performs the same configure and build process as before,
but this time with Quartermaster instrumentation. The master receives
the build information and constructs a build graph that is later used
for analysis and reporting.  Note that the project under analysis is
completely unchanged, including the build system and configuration
files.

## Step 3: Analysis

The configuration file lists a number of modules in the `package/analysis:`
section. Each of these modules are individual programs that are
shipped and installed with the master, and extend the build graph with
additional information. Usually, an individual analyzer performs a
small set of very specific tasks. The `git-analyzer` extracts the
current revision and other metadata from the repository of the project
under analysis. The `spdx-analyzer` scans input files and package
manifests for project metadata in SPDX format. The
`scancode-analyzer` identifies licenses and authors of the source
files. Most of this functionality is provided by existing
tools. Quartermaster avoids implementing features that already exist,
like a license scanner. Instead, it provides the glue code to
integrate these tools into the Quartermaster workflow and knowledge
graph. The modules mentioned above are shipped with Quartermaster. It
is however possible and intended to implement custom modules. This
will be explained in a later tutorial.

	> qmstrctl analyze
	...

The master will execute the configured analysis modules in the order
they are specified. Depending on the selected modules, this step may
take a while. All analysis results are stored within the master by
augmenting and extending the build graph generated in earlier
steps. This way, a combined picture of dynamic build-time analysis and
the results of the analyzers is created that serves as the input for
the reporters.

## Step 4: Reporting

Similarly to analyzers, reporters are configured in the
`package/reporting:` section of the configuration file. While analysis
refers to any action that augments the knowledge graph with additional
information, the graph is not allowed to change anymore during
reporting, it is frozen. Any functionality that processes the
information in the knowledge graph and performs some action based on
it is considered a report. A reporter may create output files, or
submit messages to an IRC channel, or interact with an issue tracker.

	> qmstrctl report
	...

Again, the master executes the configured reporters in order.

## Step 5: Shutting down the master

The life span of the master roughly matches that of the build
itself. The master is started right before the build is performed, and
should under normal circumstances be shut down after the build is
finished.

	> qmstrctl quit
	...

The `quit` command makes the reporting results available in the
`qmstr` subdirectory of the build directory (a different location may
be specified in the configuration file):

	> ls qmstr/qmstr-reporter-html/Public_HTML_Reports/qmstr-reports.tar.bz2
	qmstr/qmstr-reporter-html/Public_HTML_Reports/qmstr-reports.tar.bz2

Once the master is shut down, all data that it collected during the
build and analysis phases is destroyed. Any information that is
needed in later stages has to be "reported" as a build artifact by one
of the reporting modules.

