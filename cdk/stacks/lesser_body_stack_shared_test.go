package stacks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
)

func TestLesserBodySecretReadPolicyIncludesLegacyAndManagedSecretPatterns(t *testing.T) {
	assetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetDir, "bootstrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	template := synthTemplate(t, "TestStack", func(app awscdk.App) {
		stack := awscdk.NewStack(app, jsii.String("TestStack"), &awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: jsii.String("123456789012"),
				Region:  jsii.String("us-east-1"),
			},
		})

		configureLesserBodyStack(stack, &lesserBodyRuntimeProps{
			AppName:               jsii.String("theory"),
			Stage:                 jsii.String("dev"),
			Code:                  awslambda.Code_FromAsset(jsii.String(assetDir), nil),
			ServiceVersion:        jsii.String("test"),
			PublicEndpoint:        jsii.String("https://api.dev.example.com/mcp/{actor}"),
			LesserAPIBaseURL:      jsii.String("https://api.dev.example.com"),
			AllowedOrigins:        jsii.String("https://claude.ai"),
			JWTSecretArnParamPath: jsii.String("/theory/shared/secrets/jwt-secret-arn"),
			JWTSecretKeyParamPath: jsii.String("/theory/shared/kms/encryption-key-arn"),
			LesserTableParamPath:  jsii.String("/theory/dev/lesser/exports/v1/table_name"),
		})
	})

	resources := mustResources(t, template)
	statements := allPolicyStatements(t, resources)
	wantLegacy := ":secret:theory/instance-key*"
	wantManaged := ":secret:lesser-host/lab/instances/theory/instance-key*"

	if !statementResourcesContain(statements, wantLegacy) {
		t.Fatalf("expected legacy instance-key secret pattern %q in IAM policy", wantLegacy)
	}
	if !statementResourcesContain(statements, wantManaged) {
		t.Fatalf("expected managed lesser-host instance-key secret pattern %q in IAM policy", wantManaged)
	}
}

func TestManagedDeployTemplateSupportsExactLesserHostInstanceKeyARN(t *testing.T) {
	template := synthTemplate(t, "TestStack", func(app awscdk.App) {
		_ = NewLesserBodyDeployTemplateStack(app, "TestStack", &LesserBodyDeployTemplateStackProps{
			StackProps: awscdk.StackProps{
				Env: &awscdk.Environment{
					Account: jsii.String("123456789012"),
					Region:  jsii.String("us-east-1"),
				},
			},
			ServiceVersion: "test",
			Stage:          "dev",
		})
	})

	params, ok := template["Parameters"].(map[string]any)
	if !ok {
		t.Fatalf("template missing Parameters")
	}

	param, ok := params["LesserHostInstanceKeyARN"].(map[string]any)
	if !ok {
		t.Fatalf("template missing LesserHostInstanceKeyARN parameter")
	}
	if got, ok := param["Default"].(string); !ok || got != "" {
		t.Fatalf("expected LesserHostInstanceKeyARN default empty string, got %#v", param["Default"])
	}

	lambda := firstLambdaFunction(t, mustResources(t, template))
	props, ok := lambda["Properties"].(map[string]any)
	if !ok {
		t.Fatalf("lambda missing Properties")
	}
	env, ok := props["Environment"].(map[string]any)
	if !ok {
		t.Fatalf("lambda missing Environment")
	}
	vars, ok := env["Variables"].(map[string]any)
	if !ok {
		t.Fatalf("lambda missing Environment.Variables")
	}
	if !strings.Contains(mustJSON(t, vars["LESSER_HOST_INSTANCE_KEY_ARN"]), "LesserHostInstanceKeyARN") {
		t.Fatalf("expected LESSER_HOST_INSTANCE_KEY_ARN env var to reference template parameter, got %s", mustJSON(t, vars["LESSER_HOST_INSTANCE_KEY_ARN"]))
	}

	statements := allPolicyStatements(t, mustResources(t, template))
	foundConditionalExactGrant := false
	for _, statement := range statements {
		for _, resource := range extractStatementResourcesFromMap(statement) {
			if strings.Contains(mustJSON(t, resource), "LesserHostInstanceKeyARN") {
				foundConditionalExactGrant = true
				break
			}
		}
		if foundConditionalExactGrant {
			break
		}
	}
	if !foundConditionalExactGrant {
		t.Fatalf("expected an IAM secret read resource wired to LesserHostInstanceKeyARN")
	}
}

func TestLesserBodyUsesAppTheoryDurableStreamTableSchema(t *testing.T) {
	assetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetDir, "bootstrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	template := synthTemplate(t, "TestStack", func(app awscdk.App) {
		stack := awscdk.NewStack(app, jsii.String("TestStack"), &awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: jsii.String("123456789012"),
				Region:  jsii.String("us-east-1"),
			},
		})

		configureLesserBodyStack(stack, &lesserBodyRuntimeProps{
			AppName:               jsii.String("theory"),
			Stage:                 jsii.String("dev"),
			Code:                  awslambda.Code_FromAsset(jsii.String(assetDir), nil),
			ServiceVersion:        jsii.String("test"),
			PublicEndpoint:        jsii.String("https://api.dev.example.com/mcp/{actor}"),
			LesserAPIBaseURL:      jsii.String("https://api.dev.example.com"),
			AllowedOrigins:        jsii.String("https://claude.ai"),
			JWTSecretArnParamPath: jsii.String("/theory/shared/secrets/jwt-secret-arn"),
			JWTSecretKeyParamPath: jsii.String("/theory/shared/kms/encryption-key-arn"),
			LesserTableParamPath:  jsii.String("/theory/dev/lesser/exports/v1/table_name"),
		})
	})

	streamTable := findDynamoTableByName(t, mustResources(t, template), "theory-dev-mcp-streams")
	props, ok := streamTable["Properties"].(map[string]any)
	if !ok {
		t.Fatalf("stream table missing Properties")
	}

	keySchema, ok := props["KeySchema"].([]any)
	if !ok {
		t.Fatalf("stream table missing KeySchema")
	}
	if len(keySchema) != 2 {
		t.Fatalf("expected stream table hash/range keys, got %d", len(keySchema))
	}
	if !strings.Contains(mustJSON(t, keySchema[0]), `"AttributeName":"sessionId"`) {
		t.Fatalf("expected stream table hash key sessionId, got %s", mustJSON(t, keySchema[0]))
	}
	if !strings.Contains(mustJSON(t, keySchema[1]), `"AttributeName":"eventId"`) {
		t.Fatalf("expected stream table range key eventId, got %s", mustJSON(t, keySchema[1]))
	}

	lambda := firstLambdaFunction(t, mustResources(t, template))
	lambdaProps, ok := lambda["Properties"].(map[string]any)
	if !ok {
		t.Fatalf("lambda missing Properties")
	}
	env, ok := lambdaProps["Environment"].(map[string]any)
	if !ok {
		t.Fatalf("lambda missing Environment")
	}
	vars, ok := env["Variables"].(map[string]any)
	if !ok {
		t.Fatalf("lambda missing Environment.Variables")
	}
	if got, ok := vars["MCP_STREAM_TTL_MINUTES"].(string); !ok || got != "60" {
		t.Fatalf("expected MCP_STREAM_TTL_MINUTES=60, got %#v", vars["MCP_STREAM_TTL_MINUTES"])
	}
}

func TestLesserBodyRemoteMcpServerUsesActorPathWiring(t *testing.T) {
	assetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetDir, "bootstrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	template := synthTemplate(t, "TestStack", func(app awscdk.App) {
		stack := awscdk.NewStack(app, jsii.String("TestStack"), &awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: jsii.String("123456789012"),
				Region:  jsii.String("us-east-1"),
			},
		})

		configureLesserBodyStack(stack, &lesserBodyRuntimeProps{
			AppName:               jsii.String("theory"),
			Stage:                 jsii.String("dev"),
			Code:                  awslambda.Code_FromAsset(jsii.String(assetDir), nil),
			ServiceVersion:        jsii.String("test"),
			PublicEndpoint:        jsii.String("https://api.dev.example.com/mcp/{actor}"),
			LesserAPIBaseURL:      jsii.String("https://api.dev.example.com"),
			AllowedOrigins:        jsii.String("https://claude.ai"),
			JWTSecretArnParamPath: jsii.String("/theory/shared/secrets/jwt-secret-arn"),
			JWTSecretKeyParamPath: jsii.String("/theory/shared/kms/encryption-key-arn"),
			LesserTableParamPath:  jsii.String("/theory/dev/lesser/exports/v1/table_name"),
		})
	})

	templateJSON := mustJSON(t, template)
	wantPresent := []string{
		"/POST/mcp/*",
		"/GET/mcp/*",
		"/DELETE/mcp/*",
		"/GET/.well-known/oauth-protected-resource/mcp/*",
		"/.well-known/mcp.json",
	}
	for _, want := range wantPresent {
		if !strings.Contains(templateJSON, want) {
			t.Fatalf("expected synthesized template to contain %q", want)
		}
	}

	wantAbsent := []string{
		"\"/POST/mcp\"",
		"\"/GET/mcp\"",
		"\"/DELETE/mcp\"",
	}
	for _, unwanted := range wantAbsent {
		if strings.Contains(templateJSON, unwanted) {
			t.Fatalf("expected synthesized template to omit %q once actorPath wiring is enabled", unwanted)
		}
	}
}

func synthTemplate(t *testing.T, stackName string, build func(app awscdk.App)) map[string]any {
	t.Helper()

	outdir := t.TempDir()
	app := awscdk.NewApp(&awscdk.AppProps{Outdir: jsii.String(outdir)})
	build(app)
	app.Synth(nil)

	templatePath := filepath.Join(outdir, stackName+".template.json")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}

	var tpl map[string]any
	if err := json.Unmarshal(data, &tpl); err != nil {
		t.Fatalf("unmarshal template: %v", err)
	}
	return tpl
}

func mustResources(t *testing.T, tpl map[string]any) map[string]any {
	t.Helper()

	resources, ok := tpl["Resources"].(map[string]any)
	if !ok {
		t.Fatalf("template missing Resources")
	}
	return resources
}

func allPolicyStatements(t *testing.T, resources map[string]any) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, raw := range resources {
		resource, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := resource["Type"].(string); typ != "AWS::IAM::Policy" {
			continue
		}
		out = append(out, extractPolicyStatements(t, resource)...)
	}
	if len(out) == 0 {
		t.Fatalf("expected at least one IAM policy statement")
	}
	return out
}

func extractPolicyStatements(t *testing.T, policy map[string]any) []map[string]any {
	t.Helper()

	props, ok := policy["Properties"].(map[string]any)
	if !ok {
		t.Fatalf("policy missing Properties")
	}
	doc, ok := props["PolicyDocument"].(map[string]any)
	if !ok {
		t.Fatalf("policy missing PolicyDocument")
	}
	statements, ok := doc["Statement"].([]any)
	if !ok {
		t.Fatalf("policy document missing Statement list")
	}

	out := make([]map[string]any, 0, len(statements))
	for _, raw := range statements {
		statement, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("statement has unexpected type %T", raw)
		}
		out = append(out, statement)
	}
	return out
}

func statementResourcesContain(statements []map[string]any, want string) bool {
	for _, statement := range statements {
		for _, resource := range extractStatementResourcesFromMap(statement) {
			if strings.Contains(mustJSONString(resource), want) {
				return true
			}
		}
	}
	return false
}

func extractStatementResourcesFromMap(statement map[string]any) []any {
	if resources, ok := statement["Resource"].([]any); ok {
		return resources
	}
	if single, exists := statement["Resource"]; exists {
		return []any{single}
	}
	return nil
}

func firstLambdaFunction(t *testing.T, resources map[string]any) map[string]any {
	t.Helper()

	for _, raw := range resources {
		resource, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := resource["Type"].(string); typ == "AWS::Lambda::Function" {
			return resource
		}
	}
	t.Fatalf("template missing AWS::Lambda::Function resource")
	return nil
}

func findDynamoTableByName(t *testing.T, resources map[string]any, tableName string) map[string]any {
	t.Helper()

	for _, raw := range resources {
		resource, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := resource["Type"].(string); typ != "AWS::DynamoDB::Table" {
			continue
		}
		props, ok := resource["Properties"].(map[string]any)
		if !ok {
			continue
		}
		if got, _ := props["TableName"].(string); got == tableName {
			return resource
		}
	}

	t.Fatalf("template missing AWS::DynamoDB::Table %q", tableName)
	return nil
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()

	return mustJSONString(value)
}

func mustJSONString(value any) string {
	out, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(out)
}
