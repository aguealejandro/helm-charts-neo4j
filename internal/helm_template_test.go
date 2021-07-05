package internal

import (
	"bufio"
	"fmt"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"os"
	"strings"
	"testing"
)

var useEnterprise = []string{"--set", "neo4j.edition=enterprise"}
var useCommunity = []string{"--set", "neo4j.edition=community"}
var acceptLicenseAgreement = []string{"--set", "neo4j.acceptLicenseAgreement=yes"}

func TestEnterpriseThrowsErrorIfLicenseAgreementNotAccepted(t *testing.T) {
	t.Parallel()

	testCases := [][]string{
		useEnterprise,
		{"--set", "neo4j.edition=ENTERPRISE"},
		append(useEnterprise, "--set", "neo4j.acceptLicenseAgreement=absolutely"),
		append(useEnterprise, "--set", "neo4j.acceptLicenseAgreement=no"),
		append(useEnterprise, "--set", "neo4j.acceptLicenseAgreement=false"),
		append(useEnterprise, "--set", "neo4j.acceptLicenseAgreement=true"),
		append(useEnterprise, "--set", "neo4j.acceptLicenseAgreement=1"),
		append(useEnterprise, "--set", "neo4j.acceptLicenseAgreement.yes=yes"),
		append(useEnterprise, "-f", "internal/resources/acceptLicenseAgreementBoolYes.yaml"),
		append(useEnterprise, "-f", "internal/resources/acceptLicenseAgreementBoolTrue.yaml"),
	}

	doTestCase := func(t *testing.T, testCase []string) {
		t.Parallel()
		_, err := helmTemplate(t, testCase...)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "to use Neo4j Enterprise Edition you must have a Neo4j license agreement")
		assert.Contains(t, err.Error(), "Set neo4j.acceptLicenseAgreement: \"yes\" to confirm that you have a Neo4j license agreement.")
	}

	for i, testCase := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			doTestCase(t, testCase)
		})
	}
}

func TestEnterpriseDoesNotThrowErrorIfLicenseAgreementAccepted(t *testing.T) {
	t.Parallel()

	testCases := [][]string{
		append(useEnterprise, "--set", "neo4j.acceptLicenseAgreement=yes"),
		append(useEnterprise, acceptLicenseAgreement...),
		append(useEnterprise, "-f", "internal/resources/acceptLicenseAgreement.yaml"),
	}

	doTestCase := func(t *testing.T, testCase []string) {
		t.Parallel()
		manifest, err := helmTemplate(t, testCase...)
		if !assert.NoError(t, err) {
			return
		}

		checkNeo4jManifest(t, manifest)
	}

	for i, testCase := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			doTestCase(t, testCase)
		})
	}
}

// Tests the "default" behaviour that you get if you don't pass in *any* other values and the helm chart defaults are used
func TestDefaultEnterpriseHelmTemplate(t *testing.T) {
	t.Parallel()

	manifest, err := helmTemplate(t, append(useEnterprise, acceptLicenseAgreement...)...)
	if !assert.NoError(t, err) {
		return
	}

	checkNeo4jManifest(t, manifest)

	neo4jStatefulSet := manifest.first(&appsv1.StatefulSet{}).(*appsv1.StatefulSet)
	for _, container := range neo4jStatefulSet.Spec.Template.Spec.Containers {
		assert.Contains(t, container.Image, "enterprise")
	}
	for _, container := range neo4jStatefulSet.Spec.Template.Spec.InitContainers {
		assert.Contains(t, container.Image, "enterprise")
	}
}

// Tests the "default" behaviour that you get if you don't pass in *any* other values and the helm chart defaults are used
func TestDefaultCommunityHelmTemplate(t *testing.T) {
	t.Parallel()

	manifest, err := helmTemplate(t)
	if !assert.NoError(t, err) {
		return
	}

	checkNeo4jManifest(t, manifest)

	neo4jStatefulSet := manifest.first(&appsv1.StatefulSet{}).(*appsv1.StatefulSet)
	neo4jStatefulSet.GetName()
	for _, container := range neo4jStatefulSet.Spec.Template.Spec.Containers {
		assert.NotContains(t, container.Image, "enterprise")
	}
	for _, container := range neo4jStatefulSet.Spec.Template.Spec.InitContainers {
		assert.NotContains(t, container.Image, "enterprise")
	}

	envConfigMap := manifest.ofTypeWithName(&v1.ConfigMap{}, DefaultHelmTemplateReleaseName.envConfigMapName()).(*v1.ConfigMap)
	assert.Equal(t, envConfigMap.Data["NEO4J_EDITION"], "COMMUNITY_K8S")
}

func TestAdditionalEnvVars(t *testing.T) {
	t.Parallel()

	manifest, err := helmTemplate(t, "--set", "env.FOO=one", "--set", "env.GRAPHS=are everywhere")
	if !assert.NoError(t, err) {
		return
	}

	envConfigMap := manifest.ofTypeWithName(&v1.ConfigMap{}, DefaultHelmTemplateReleaseName.envConfigMapName()).(*v1.ConfigMap)
	assert.Equal(t, envConfigMap.Data["FOO"], "one")
	assert.Equal(t, envConfigMap.Data["GRAPHS"], "are everywhere")

	checkNeo4jManifest(t, manifest)
}

func TestJvmAdditionalConfig(t *testing.T) {
	t.Parallel()

	testCases := []string{"community", "enterprise"}

	for _, edition := range testCases {
		t.Run(t.Name() + edition, func(t *testing.T) {
			manifest, err := helmTemplate(t,
				"-f", "internal/resources/jvmAdditionalSettings.yaml",
				"--set", "neo4j.edition="+edition,
				"--set", "neo4j.acceptLicenseAgreement=yes",
			)
			if !assert.NoError(t, err) {
				return
			}

			userConfigMap := manifest.ofTypeWithName(&v1.ConfigMap{}, DefaultHelmTemplateReleaseName.userConfigMapName()).(*v1.ConfigMap)
			assert.Contains(t, userConfigMap.Data["dbms.jvm.additional"], "-XX:+HeapDumpOnOutOfMemoryError")
			assert.Contains(t, userConfigMap.Data["dbms.jvm.additional"], "-XX:HeapDumpPath=./java_pid<pid>.hprof")
			assert.Contains(t, userConfigMap.Data["dbms.jvm.additional"], "-XX:+UseGCOverheadLimit")

			err = checkConfigMapContainsJvmAdditionalFromDefaultConf(t, edition, userConfigMap)
			if !assert.NoError(t, err) {
				return
			}

			checkNeo4jManifest(t, manifest)
		})
	}

}

func checkConfigMapContainsJvmAdditionalFromDefaultConf(t *testing.T, edition string, userConfigMap *v1.ConfigMap) error {
	// check that we picked up jvm additional from the conf file
	file, err := os.Open(fmt.Sprintf("neo4j-standalone/neo4j-%s.conf", edition))
	defer file.Close()
	if err != nil {
		return err
	}

	n := 0
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		var line = scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "dbms.jvm.additional") {
			line = strings.Replace(line, "dbms.jvm.additional=", "", 1)
			assert.Contains(t, userConfigMap.Data["dbms.jvm.additional"], line)
			n++
		}
		if err != nil {
			return err
		}

	}
	// The conf file should contain at least 4 (this just sanity checks that the scanner and string handling stuff above didn't screw up)
	assert.GreaterOrEqual(t, n, 4)
	return nil
}

func TestBoolsInConfig(t *testing.T) {
	t.Parallel()

	_, err := helmTemplate(t, "-f", "internal/resources/boolsInConfig.yaml")
	assert.Error(t, err, "Helm chart should fail if config contains boolean values")
	assert.Contains(t, err.Error(), "config values must be strings.")
	assert.Contains(t, err.Error(), "metrics.enabled")
	assert.Contains(t, err.Error(), "type: bool")
}

func TestIntsInConfig(t *testing.T) {
	t.Parallel()

	_, err := helmTemplate(t, "-f", "internal/resources/intsInConfig.yaml")
	assert.Error(t, err, "Helm chart should fail if config contains int values")
	assert.Contains(t, err.Error(), "config values must be strings.")
	assert.Contains(t, err.Error(), "metrics.csv.rotation.keep_number")
	assert.Contains(t, err.Error(), "type: float64")
}

// Tests the "default" behaviour that you get if you don't pass in *any* other values and the helm chart defaults are used
func TestChmodInitContainer(t *testing.T) {
	t.Parallel()

	manifest, err := helmTemplate(t, "-f", "internal/resources/chmodInitContainer.yaml")
	if !assert.NoError(t, err) {
		return
	}

	checkNeo4jManifest(t, manifest)

	neo4jStatefulSet := manifest.first(&appsv1.StatefulSet{}).(*appsv1.StatefulSet)
	initContainers := neo4jStatefulSet.Spec.Template.Spec.InitContainers
	assert.Len(t, initContainers, 1)
	container := initContainers[0]
	assert.Equal(t, "set-volume-permissions", container.Name)
	assert.Len(t, container.VolumeMounts, 6)
	// Command will chown logs
	assert.Contains(t, container.Command[2], "chown -R \"7474\" \"/logs\"")
	assert.Contains(t, container.Command[2], "chgrp -R \"7474\" \"/logs\"")
	assert.Contains(t, container.Command[2], "chmod -R g+rwx \"/logs\"")
	// Command will not chown data
	assert.NotContains(t, container.Command[2], "chown -R \"7474\" \"/data\"")
	assert.NotContains(t, container.Command[2], "chgrp -R \"7474\" \"/data\"")
	assert.NotContains(t, container.Command[2], "chmod -R g+rwx \"/data\"")
}

// Tests the "default" behaviour that you get if you don't pass in *any* other values and the helm chart defaults are used
func TestChmodInitContainers(t *testing.T) {
	t.Parallel()

	manifest, err := helmTemplate(t, "-f", "internal/resources/chmodInitContainerAndCustomInitContainer.yaml")
	if !assert.NoError(t, err) {
		return
	}

	checkNeo4jManifest(t, manifest)

	neo4jStatefulSet := manifest.first(&appsv1.StatefulSet{}).(*appsv1.StatefulSet)
	initContainers := neo4jStatefulSet.Spec.Template.Spec.InitContainers
	assert.Len(t, initContainers, 2)
	container := initContainers[0]
	assert.Equal(t, "set-volume-permissions", container.Name)
	assert.Len(t, container.VolumeMounts, 6)
	// Command will chown logs
	assert.Contains(t, container.Command[2], "chown -R \"7474\" \"/logs\"")
	assert.Contains(t, container.Command[2], "chgrp -R \"7474\" \"/logs\"")
	assert.Contains(t, container.Command[2], "chmod -R g+rwx \"/logs\"")
	// Command will not chown data
	assert.NotContains(t, container.Command[2], "chown -R \"7474\" \"/data\"")
	assert.NotContains(t, container.Command[2], "chgrp -R \"7474\" \"/data\"")
	assert.NotContains(t, container.Command[2], "chmod -R g+rwx \"/data\"")
}

// Tests the "default" behaviour that you get if you don't pass in *any* other values and the helm chart defaults are used
func TestExplicitCommunityHelmTemplate(t *testing.T) {
	t.Parallel()

	manifest, err := helmTemplate(t, useCommunity...)
	if !assert.NoError(t, err) {
		return
	}

	checkNeo4jManifest(t, manifest)

	neo4jStatefulSet := manifest.first(&appsv1.StatefulSet{}).(*appsv1.StatefulSet)
	neo4jStatefulSet.GetName()
	for _, container := range neo4jStatefulSet.Spec.Template.Spec.Containers {
		assert.NotContains(t, container.Image, "enterprise")
	}
	for _, container := range neo4jStatefulSet.Spec.Template.Spec.InitContainers {
		assert.NotContains(t, container.Image, "enterprise")
	}

	envConfigMap := manifest.ofTypeWithName(&v1.ConfigMap{}, DefaultHelmTemplateReleaseName.envConfigMapName()).(*v1.ConfigMap)
	assert.Equal(t, envConfigMap.Data["NEO4J_EDITION"], "COMMUNITY_K8S")
}

// Tests the "base" helm command used for Integration Tests
func TestBaseHelmTemplate(t *testing.T) {
	t.Parallel()

	extraArgs := []string{}

	_, err := helmTemplate(t, baseHelmCommand("template", &DefaultHelmTemplateReleaseName, extraArgs...)...)
	if !assert.NoError(t, err) {
		return
	}
}

func checkNeo4jManifest(t *testing.T, manifest *K8sResources) {
	// should contain exactly one StatefulSet
	assert.Len(t, manifest.ofType(&appsv1.StatefulSet{}), 1)

	assertOnlyNeo4jImagesUsed(t, manifest)

	assertThreeServices(t, manifest)

	assertFourConfigMaps(t, manifest)
}

func assertFourConfigMaps(t *testing.T, manifest *K8sResources) {
	services := manifest.ofType(&v1.ConfigMap{})
	assert.Len(t, services, 4)
}

func assertThreeServices(t *testing.T, manifest *K8sResources) {
	services := manifest.ofType(&v1.Service{})
	assert.Len(t, services, 3)
}

func assertOnlyNeo4jImagesUsed(t *testing.T, manifest *K8sResources) {
	for _, neo4jStatefulSet := range manifest.ofType(&appsv1.StatefulSet{}) {
		assertOnlyNeo4jImagesUsedInStatefulSet(t, neo4jStatefulSet.(*appsv1.StatefulSet))
	}
	//TODO: add checks on Pods, Jobs, CronJobs, ReplicaSets, Deployments and anything else that can contain an image
}

func assertOnlyNeo4jImagesUsedInStatefulSet(t *testing.T, neo4jStatefulSet *appsv1.StatefulSet) {
	for _, container := range neo4jStatefulSet.Spec.Template.Spec.Containers {
		assert.Contains(t, container.Image, "neo4j:")
	}

	for _, container := range neo4jStatefulSet.Spec.Template.Spec.InitContainers {
		assert.Contains(t, container.Image, "neo4j:")
	}
}
