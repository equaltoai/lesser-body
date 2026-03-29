package main

import (
	"flag"
	"fmt"
	"os"

	"cdk/stacks"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	defer jsii.Close()

	var (
		outdir  string
		version string
		stage   string
	)

	flag.StringVar(&outdir, "outdir", "", "cloud assembly output directory")
	flag.StringVar(&version, "version", "", "release version injected into SERVICE_VERSION")
	flag.StringVar(&stage, "stage", "", "managed deploy template stage: dev | staging | live")
	flag.Parse()

	if outdir == "" {
		fmt.Fprintln(os.Stderr, "--outdir is required")
		os.Exit(1)
	}
	if version == "" {
		fmt.Fprintln(os.Stderr, "--version is required")
		os.Exit(1)
	}
	if stage == "" {
		fmt.Fprintln(os.Stderr, "--stage is required")
		os.Exit(1)
	}

	app := awscdk.NewApp(&awscdk.AppProps{
		Outdir:             &outdir,
		AnalyticsReporting: jsii.Bool(false),
		AutoSynth:          jsii.Bool(false),
		TreeMetadata:       jsii.Bool(false),
	})

	stacks.NewLesserBodyDeployTemplateStack(app, "LesserBodyManagedTemplate", &stacks.LesserBodyDeployTemplateStackProps{
		ServiceVersion: version,
		Stage:          stage,
	})

	app.Synth(nil)
}
