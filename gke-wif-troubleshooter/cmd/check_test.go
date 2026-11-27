// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	cloudiam "cloud.google.com/go/iam"
	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestGetKubeconfig(t *testing.T) {
	origKubeconfigPath := kubeconfigpath
	t.Cleanup(func() { kubeconfigpath = origKubeconfigPath })

	t.Run("Success", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("HOME", tempDir)
		kubeDir := filepath.Join(tempDir, ".kube")
		if err := os.Mkdir(kubeDir, 0755); err != nil {
			t.Fatalf("os.Mkdir(%q) unexpected error: %v", kubeDir, err)
		}
		configPath := filepath.Join(kubeDir, "config")
		if err := os.WriteFile(configPath, []byte("test-config"), 0644); err != nil {
			t.Fatalf("os.WriteFile(%q) unexpected error: %v", configPath, err)
		}

		kubeconfigpath = ""
		if err := getKubeconfig("true"); err != nil {
			t.Fatalf("getKubeconfig(\"true\") unexpected error: %v", err)
		}
		if kubeconfigpath != configPath {
			t.Errorf(`after getKubeconfig("true"), kubeconfigpath = %q, want %q`, kubeconfigpath, configPath)
		}
	})

	t.Run("DisabledClearsPath", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("HOME", tempDir)
		kubeconfigpath = "/some/previous/path"

		if err := getKubeconfig("false"); err != nil {
			t.Fatalf(`getKubeconfig("false") unexpected error: %v`, err)
		}
		if kubeconfigpath != "" {
			t.Errorf(`after getKubeconfig("false"), kubeconfigpath = %q, want ""`, kubeconfigpath)
		}
	})

	t.Run("InvalidBool", func(t *testing.T) {
		kubeconfigpath = "/existing/path"

		if err := getKubeconfig("not-a-bool"); err == nil {
			t.Fatal(`getKubeconfig("not-a-bool") = nil, want error`)
		}
		if kubeconfigpath != "/existing/path" {
			t.Errorf(`after getKubeconfig("not-a-bool"), kubeconfigpath = %q, want %q`, kubeconfigpath, "/existing/path")
		}
	})

	t.Run("NoHomeDir", func(t *testing.T) {
		origHome, hadHome := os.LookupEnv("HOME")
		t.Cleanup(func() {
			if hadHome {
				os.Setenv("HOME", origHome)
			}
		})
		os.Unsetenv("HOME")

		if _, err := os.UserHomeDir(); err == nil {
			t.Skip("Skipping test: unable to reliably trigger UserHomeDir error")
		}
		if err := getKubeconfig("true"); err == nil {
			t.Fatal(`getKubeconfig("true") with unset HOME = nil, want error`)
		}
	})

	t.Run("KubeconfigNotFound", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("HOME", tempDir)
		kubeconfigpath = ""

		if err := getKubeconfig("true"); err == nil {
			t.Fatal(`getKubeconfig("true") with missing file = nil, want error`)
		}
		if kubeconfigpath != "" {
			t.Errorf(`after getKubeconfig("true") with missing file, kubeconfigpath = %q, want ""`, kubeconfigpath)
		}
	})
}

func TestGetK8sClientset(t *testing.T) {
	cluster := &containerpb.Cluster{
		Endpoint: "localhost:8080",
		MasterAuth: &containerpb.MasterAuth{
			ClusterCaCertificate: "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUVMVENDQXBXZ0F3SUJBZ0lSQU9OUVQ4QXh2OWk0WURpenFrVEJidEV3RFFZSktvWklodmNOQVFFTEJRQXcKTHpFdE1Dc0dBMVVFQXhNa1pHUmlOV0UwWkdRdE1EQTRNUzAwT1RZMUxXRXdNakF0TWpoaE9UazBPVEJpTnpZNApNQ0FYRFRJMU1EVXhNakF5TWpjMU1Gb1lEekl3TlRVd05UQTFNRE15TnpVd1dqQXZNUzB3S3dZRFZRUURFeVJrClpHSTFZVFJrWkMwd01EZ3hMVFE1TmpVdFlUQXlNQzB5T0dFNU9UUTVNR0kzTmpnd2dnR2lNQTBHQ1NxR1NJYjMKRFFFQkFRVUFBNElCandBd2dnR0tBb0lCZ1FEc1p4TllYR2NmN0dJOVA2eFVRanhlYTRMY0N0d01pcmNZUWtVOApRcG82bUFoMkZoKzJvQkZlNFhzb2Z3QWdmSnFWODdEdmlZdC9RV3BiYlovY1drMHBjcnNzaWNjUzV0QXQ5NGhmClBMY09FYkJ6QWJBY1ozbXFHeGtBNjZ5YWlmbnd4RGROYVkxSmRPakRMZnUyd0t5M3lZdXdtUHYycFFob0NQVmEKaUd3bzlIRWt2Uk0yWkdQZzVOdFJVZ2NGb2c5Ukk3a3BVK1NJUkZ6MlRaakNSc0Z1SVl1VERzR0dEaHlTazQxUwpvOVpUN05lMUxaY2k4dWpMNzZNbVRZbXpHblhjcjRsdFdqY3MxYkZCN3RnUFZkZnBVV1dJdDdkNm1BQVhBUjJVCmp5RThZNWF6cmJ3YTl3cVd3N2hHVG9WZkhtdUFvdTUzQ3F6VkxjSEdlYnJnN3J2MGU0MHQxcncySGtCaG55bGkKRFUwc3R4SlNEeEhoU1A0aWRHQVkwaG5WVUJpTU9HdUVWeWpKaFlqS3h4d2JOR21zN2loZ3RtdFBnb1ZMRmFDZQpjRlFiQ0pKcFVZbDZFZ01jMkQyMVJyRUZXR1V4SlFEbnZwQ0g1TTViTTQxRlBNUEJqMVc1TzRGMmY1Wjc1ajdMCnNhazU5Y0FIam9FNDNKczlwenMxbHZsTDc5MENBd0VBQWFOQ01FQXdEZ1lEVlIwUEFRSC9CQVFEQWdJRU1BOEcKQTFVZEV3RUIvd1FGTUFNQkFmOHdIUVlEVlIwT0JCWUVGRDhGQzhCOXlHdTdqeE5yZGlQQUZMSzlVSXhvTUEwRwpDU3FHU0liM0RRRUJDd1VBQTRJQmdRQklHbUZrTStkcDRqYXFCK3RMTkdPTHZLWUJpaDFpemdhVzZWenFoUkl3CnlJQ1ZNYno1MzY2aW1ySjkxMVdaQ3o1U3NrdXRGQVJ0VTVGd01pVjZBTjFxUExmbndyMHdmTkVkUFJEUGovWEsKYzNnbEl6RGFkNnZVYkxZRmxlVDBPMnk4TEVmaVhPMzNYNXNOTHNsQkxXNCttenBUWHYwTnRveGRRS3lxWDE1cwo5bWcxalEwQzFDRDZzdHJ3RU9PcGZqcGRpSUZBZHYzMkVKSExPbjN1d2xraURpOTlQUks1NTV6NE5sS3VrTG9HCk9mVUorNmdsaGltZ1A2LytSWnY4eE92M2R2bHZqckZKUndiUS8rTnNhUFRwTlRyMk5tenFqdDdUU2FubXBsMzgKYXJ4NEh5T1Z3YTZaWS92V0l1R2pROUt3d0dPU3IrTGtyU3l1Smw1Rm9TVll1MkZuYnNPQVJObEhXYXNlNC8rTwp5eFZVMHU2ckREVG4zRGtjc09KSUluOXNOU3dRUW9zbXB4MC9peGtIa2JnejI5bklwVlAyV0lacUxRSFFBeEsxClh3ZGZaZ3JCdTF5N0M0ejRNNlRhT3ZFT0I1YlR5YzE3TExJN2RhcHpJWCsyV09UN1dCTDBJNzRxVGJBSHN2TVQKOHZQZWk0cFNMK0hOMm5RQmxEK2JmWTQ9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K",
		},
	}

	t.Run("FromClusterConfig", func(t *testing.T) {
		kubeconfigpath = ""
		clientset, err := getK8sClientset(cluster)
		assert.NoError(t, err)
		assert.NotNil(t, clientset)
	})

	t.Run("FromKubeconfigPath", func(t *testing.T) {
		cfg := clientcmdapi.Config{
			Clusters: map[string]*clientcmdapi.Cluster{
				"test-cluster": {
					Server: "https://localhost",
				},
			},
			Contexts: map[string]*clientcmdapi.Context{
				"test-context": {
					Cluster: "test-cluster",
				},
			},
			CurrentContext: "test-context",
		}
		tmpfile, err := os.CreateTemp("", "kubeconfig")
		assert.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		err = clientcmd.WriteToFile(cfg, tmpfile.Name())
		assert.NoError(t, err)

		kubeconfigpath = tmpfile.Name()
		clientset, err := getK8sClientset(cluster)
		assert.NoError(t, err)
		assert.NotNil(t, clientset)
	})

	t.Run("FromKubeconfigPathError", func(t *testing.T) {
		kubeconfigpath = "/path/to/non/existent/config"
		_, err := getK8sClientset(cluster)
		assert.Error(t, err)
	})

	t.Run("InvalidBase64Cert", func(t *testing.T) {
		kubeconfigpath = ""
		invalidCluster := &containerpb.Cluster{
			Endpoint: "localhost:8080",
			MasterAuth: &containerpb.MasterAuth{
				ClusterCaCertificate: "invalid-base64",
			},
		}
		_, err := getK8sClientset(invalidCluster)
		assert.Error(t, err)
	})
}

func TestBuildK8sRESTConfig(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	const validCertB64 = "dGVzdC1jYQ==" // "test-ca"

	tempDir := t.TempDir()
	kubeconfigFile := filepath.Join(tempDir, "kubeconfig")
	if err := clientcmd.WriteToFile(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"local": {Server: "https://127.0.0.1:6443"},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"local": {Cluster: "local"},
		},
		CurrentContext: "local",
	}, kubeconfigFile); err != nil {
		t.Fatalf("clientcmd.WriteToFile(%q) unexpected error: %v", kubeconfigFile, err)
	}

	tests := []struct {
		name           string
		cluster        *containerpb.Cluster
		kubeconfigPath string
		wantHost       string
		wantHasCAData  bool
		wantWarnSubstr string
		wantErr        error
	}{
		{
			name:           "KubeconfigPathOverridesCluster",
			kubeconfigPath: kubeconfigFile,
			wantHost:       "https://127.0.0.1:6443",
			wantHasCAData:  false,
		},
		{
			name:           "ErrorWhenKubeconfigMissing",
			kubeconfigPath: filepath.Join(tempDir, "missing"),
			wantErr:        os.ErrNotExist,
		},
		{
			name: "DNSEndpointPreferredWhenExternalTrafficAllowed",
			cluster: &containerpb.Cluster{
				Name:     "dns-cluster",
				Endpoint: "34.1.2.3",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				ControlPlaneEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig{
					DnsEndpointConfig: &containerpb.ControlPlaneEndpointsConfig_DNSEndpointConfig{
						Endpoint:             "abc123.us-central1.gke.goog",
						AllowExternalTraffic: boolPtr(true),
					},
					IpEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig_IPEndpointsConfig{
						Enabled:              boolPtr(true),
						EnablePublicEndpoint: boolPtr(true),
						PublicEndpoint:       "34.1.2.3",
						PrivateEndpoint:      "10.0.0.2",
					},
				},
			},
			wantHost:      "https://abc123.us-central1.gke.goog",
			wantHasCAData: false,
		},
		{
			name: "DNSOnlyClusterWhenIPEndpointsDisabled",
			cluster: &containerpb.Cluster{
				Name: "dns-only-cluster",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				ControlPlaneEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig{
					DnsEndpointConfig: &containerpb.ControlPlaneEndpointsConfig_DNSEndpointConfig{
						Endpoint:             "dns-only.us-central1.gke.goog",
						AllowExternalTraffic: boolPtr(true),
					},
					IpEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig_IPEndpointsConfig{
						Enabled: boolPtr(false),
					},
				},
			},
			wantHost:      "https://dns-only.us-central1.gke.goog",
			wantHasCAData: false,
		},
		{
			name: "ExternalIPUsedWhenDNSExternalTrafficDisabled",
			cluster: &containerpb.Cluster{
				Name:     "public-ip-cluster",
				Endpoint: "34.1.2.3",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				ControlPlaneEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig{
					DnsEndpointConfig: &containerpb.ControlPlaneEndpointsConfig_DNSEndpointConfig{
						Endpoint:             "abc123.us-central1.gke.goog",
						AllowExternalTraffic: boolPtr(false),
					},
					IpEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig_IPEndpointsConfig{
						Enabled:              boolPtr(true),
						EnablePublicEndpoint: boolPtr(true),
						PublicEndpoint:       "34.1.2.3",
						PrivateEndpoint:      "10.0.0.2",
					},
				},
			},
			wantHost:      "https://34.1.2.3",
			wantHasCAData: true,
		},
		{
			name: "InternalIPFallbackWithWarningWhenPublicEndpointDisabled",
			cluster: &containerpb.Cluster{
				Name:     "private-ip-cluster",
				Endpoint: "10.0.0.2",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				ControlPlaneEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig{
					DnsEndpointConfig: &containerpb.ControlPlaneEndpointsConfig_DNSEndpointConfig{
						Endpoint:             "abc123.us-central1.gke.goog",
						AllowExternalTraffic: boolPtr(false),
					},
					IpEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig_IPEndpointsConfig{
						Enabled:              boolPtr(true),
						EnablePublicEndpoint: boolPtr(false),
						PrivateEndpoint:      "10.0.0.2",
					},
				},
			},
			wantHost:       "https://10.0.0.2",
			wantHasCAData:  true,
			wantWarnSubstr: "falling back to internal endpoint",
		},
		{
			name: "InternalIPFallbackWithWarningWhenPublicEndpointEmpty",
			cluster: &containerpb.Cluster{
				Name: "private-only-ip-cluster",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				ControlPlaneEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig{
					IpEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig_IPEndpointsConfig{
						Enabled:         boolPtr(true),
						PrivateEndpoint: "10.0.0.5",
					},
				},
			},
			wantHost:       "https://10.0.0.5",
			wantHasCAData:  true,
			wantWarnSubstr: "not reachable to the cluster's private endpoint",
		},
		{
			name: "IPEndpointsConfigFallsBackToClusterEndpointBeforePrivateWhenPublicEnabled",
			cluster: &containerpb.Cluster{
				Name:     "ip-cfg-cluster-endpoint-fallback",
				Endpoint: "34.2.3.4",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				ControlPlaneEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig{
					IpEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig_IPEndpointsConfig{
						Enabled:              boolPtr(true),
						EnablePublicEndpoint: boolPtr(true),
						PrivateEndpoint:      "10.0.0.6",
					},
				},
			},
			wantHost:      "https://34.2.3.4",
			wantHasCAData: true,
		},
		{
			name: "IPEndpointsConfigFallsBackToClusterEndpointAsInternalWhenPublicDisabled",
			cluster: &containerpb.Cluster{
				Name:     "ip-cfg-private-cluster-endpoint-fallback",
				Endpoint: "10.0.0.7",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				ControlPlaneEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig{
					IpEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig_IPEndpointsConfig{
						Enabled:              boolPtr(true),
						EnablePublicEndpoint: boolPtr(false),
					},
				},
			},
			wantHost:       "https://10.0.0.7",
			wantHasCAData:  true,
			wantWarnSubstr: "falling back to internal endpoint",
		},
		{
			name: "LegacyPrivateClusterConfigUsesExternalWhenEnabled",
			cluster: &containerpb.Cluster{
				Name: "legacy-public-cluster",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				PrivateClusterConfig: &containerpb.PrivateClusterConfig{
					EnablePrivateEndpoint: false,
					PublicEndpoint:        "35.1.2.3",
					PrivateEndpoint:       "10.1.0.2",
				},
			},
			wantHost:      "https://35.1.2.3",
			wantHasCAData: true,
		},
		{
			name: "LegacyPrivateClusterConfigFallsBackToClusterEndpointBeforePrivateWhenPublicEnabled",
			cluster: &containerpb.Cluster{
				Name:     "legacy-public-cluster-endpoint-fallback",
				Endpoint: "35.2.3.4",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				PrivateClusterConfig: &containerpb.PrivateClusterConfig{
					EnablePrivateEndpoint: false,
					PrivateEndpoint:       "10.1.0.3",
				},
			},
			wantHost:      "https://35.2.3.4",
			wantHasCAData: true,
		},
		{
			name: "LegacyPrivateClusterConfigFallsBackToClusterEndpointAsInternalWhenPrivateEnabled",
			cluster: &containerpb.Cluster{
				Name:     "legacy-private-cluster-endpoint-fallback",
				Endpoint: "10.1.0.4",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				PrivateClusterConfig: &containerpb.PrivateClusterConfig{
					EnablePrivateEndpoint: true,
				},
			},
			wantHost:       "https://10.1.0.4",
			wantHasCAData:  true,
			wantWarnSubstr: "falling back to internal endpoint",
		},
		{
			name: "LegacyPrivateClusterConfigFallsBackToInternalWithWarning",
			cluster: &containerpb.Cluster{
				Name: "legacy-private-cluster",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
				PrivateClusterConfig: &containerpb.PrivateClusterConfig{
					EnablePrivateEndpoint: true,
					PublicEndpoint:        "35.1.2.3",
					PrivateEndpoint:       "10.1.0.2",
				},
			},
			wantHost:       "https://10.1.0.2",
			wantHasCAData:  true,
			wantWarnSubstr: "falling back to internal endpoint",
		},
		{
			name: "TopLevelEndpointFallback",
			cluster: &containerpb.Cluster{
				Name:     "simple-cluster",
				Endpoint: "34.9.9.9",
				MasterAuth: &containerpb.MasterAuth{
					ClusterCaCertificate: validCertB64,
				},
			},
			wantHost:      "https://34.9.9.9",
			wantHasCAData: true,
		},
		{
			name: "ErrorWhenDNSAndIPBothDisabled",
			cluster: &containerpb.Cluster{
				Name: "no-access-cluster",
				ControlPlaneEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig{
					DnsEndpointConfig: &containerpb.ControlPlaneEndpointsConfig_DNSEndpointConfig{
						Endpoint:             "abc123.us-central1.gke.goog",
						AllowExternalTraffic: boolPtr(false),
					},
					IpEndpointsConfig: &containerpb.ControlPlaneEndpointsConfig_IPEndpointsConfig{
						Enabled: boolPtr(false),
					},
				},
			},
			wantErr: errEndpointDisabled,
		},
		{
			name:    "ErrorWhenNoEndpointConfigured",
			cluster: &containerpb.Cluster{Name: "empty-cluster"},
			wantErr: errNoEndpointFound,
		},
		{
			name:    "ErrorWhenClusterNil",
			cluster: nil,
			wantErr: errNilCluster,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var warnBuf bytes.Buffer
			cfg, err := buildK8sRESTConfig(tc.cluster, tc.kubeconfigPath, &warnBuf)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("buildK8sRESTConfig(%q, %q) err = %v, want %v", tc.cluster.GetName(), tc.kubeconfigPath, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildK8sRESTConfig(%q, %q) unexpected error: %v", tc.cluster.GetName(), tc.kubeconfigPath, err)
			}

			if cfg.Host != tc.wantHost {
				t.Errorf("buildK8sRESTConfig(%q, %q).Host = %q, want %q", tc.cluster.GetName(), tc.kubeconfigPath, cfg.Host, tc.wantHost)
			}
			if cfg.UserAgent != userAgent() {
				t.Errorf("buildK8sRESTConfig(%q, %q).UserAgent = %q, want %q", tc.cluster.GetName(), tc.kubeconfigPath, cfg.UserAgent, userAgent())
			}
			gotHasCAData := len(cfg.TLSClientConfig.CAData) > 0
			if gotHasCAData != tc.wantHasCAData {
				t.Errorf("buildK8sRESTConfig(%q, %q).TLSClientConfig.CAData present = %t, want %t", tc.cluster.GetName(), tc.kubeconfigPath, gotHasCAData, tc.wantHasCAData)
			}
			if tc.wantWarnSubstr != "" {
				if !strings.Contains(warnBuf.String(), tc.wantWarnSubstr) {
					t.Errorf("buildK8sRESTConfig(%q, %q) warning = %q, want substring %q", tc.cluster.GetName(), tc.kubeconfigPath, warnBuf.String(), tc.wantWarnSubstr)
				}
			} else if warnBuf.Len() > 0 {
				t.Errorf("buildK8sRESTConfig(%q, %q) unexpected warning: %q", tc.cluster.GetName(), tc.kubeconfigPath, warnBuf.String())
			}
		})
	}
}

type fakeGCPIAMPolicyReader struct {
	projectPolicy *iampb.Policy
	projectErr    error
	gsaPolicy     *cloudiam.Policy
	gsaErr        error

	gotProject     string
	gotGSAResource string
}

func (f *fakeGCPIAMPolicyReader) ProjectIAMPolicy(_ context.Context, project string) (*iampb.Policy, error) {
	f.gotProject = project
	return f.projectPolicy, f.projectErr
}

func (f *fakeGCPIAMPolicyReader) GSAIAMPolicy(_ context.Context, resource string) (*cloudiam.Policy, error) {
	f.gotGSAResource = resource
	return f.gsaPolicy, f.gsaErr
}

func policyWithWIFMember(member string) *cloudiam.Policy {
	p := &cloudiam.Policy{}
	p.Add(member, "roles/iam.workloadIdentityUser")
	return p
}

func TestKSACheckerCheck(t *testing.T) {
	defaultCluster := &containerpb.Cluster{
		Name:     "test-cluster",
		Location: "us-central1",
		WorkloadIdentityConfig: &containerpb.WorkloadIdentityConfig{
			WorkloadPool: "cluster-project.svc.id.goog",
		},
	}
	fleetCluster := &containerpb.Cluster{
		Name:     "fleet-cluster",
		Location: "us-central1",
		WorkloadIdentityConfig: &containerpb.WorkloadIdentityConfig{
			WorkloadPool: "fleet-host-proj.svc.id.goog",
		},
	}
	clusterWithoutWI := &containerpb.Cluster{
		Name:     "test-cluster-no-wi",
		Location: "us-central1",
	}

	target := ksaTarget{namespace: "prod", name: "app-ksa"}
	unannotatedKSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: target.name, Namespace: target.namespace},
	}
	sameProjectAnnotatedKSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        target.name,
			Namespace:   target.namespace,
			Annotations: map[string]string{"iam.gke.io/gcp-service-account": "app-sa@cluster-project.iam.gserviceaccount.com"},
		},
	}
	crossProjectAnnotatedKSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        target.name,
			Namespace:   target.namespace,
			Annotations: map[string]string{"iam.gke.io/gcp-service-account": "shared-sa@gsa-owner-proj.iam.gserviceaccount.com"},
		},
	}

	tests := []struct {
		name               string
		cluster            *containerpb.Cluster
		ksa                *corev1.ServiceAccount
		iamReader          *fakeGCPIAMPolicyReader
		wantErrContains    []string
		wantOutContains    []string
		wantProjectQueried string
		wantGSAQueried     string
	}{
		{
			name:            "WorkloadIdentityDisabled",
			cluster:         clusterWithoutWI,
			iamReader:       &fakeGCPIAMPolicyReader{},
			wantErrContains: []string{"Workload Identity is not enabled"},
		},
		{
			name:            "KSANotFound",
			cluster:         defaultCluster,
			iamReader:       &fakeGCPIAMPolicyReader{},
			wantErrContains: []string{"getting Kubernetes Service Account"},
		},
		{
			name:    "DirectProjectBindingWithFleetWorkloadPool",
			cluster: fleetCluster,
			ksa:     unannotatedKSA,
			iamReader: &fakeGCPIAMPolicyReader{
				projectPolicy: &iampb.Policy{
					Bindings: []*iampb.Binding{{
						Role: "roles/storage.objectViewer",
						Members: []string{
							"principal://iam.googleapis.com/projects/123456/locations/global/workloadIdentityPools/fleet-host-proj.svc.id.goog/subject/ns/prod/sa/app-ksa",
						},
					}},
				},
			},
			wantOutContains: []string{
				"Found direct IAM bindings for KSA principal",
				"fleet-host-proj.svc.id.goog/subject/ns/prod/sa/app-ksa",
			},
			wantProjectQueried: "cluster-project",
		},
		{
			name:    "DirectProjectBindingMissing",
			cluster: defaultCluster,
			ksa:     unannotatedKSA,
			iamReader: &fakeGCPIAMPolicyReader{
				projectPolicy: &iampb.Policy{},
			},
			wantOutContains:    []string{"No direct IAM bindings found for KSA principal"},
			wantProjectQueried: "cluster-project",
		},
		{
			name:    "DirectProjectIAMPolicyError",
			cluster: defaultCluster,
			ksa:     unannotatedKSA,
			iamReader: &fakeGCPIAMPolicyReader{
				projectErr: status.Error(codes.PermissionDenied, "denied"),
			},
			wantErrContains:    []string{`getting IAM policy for project "cluster-project"`},
			wantProjectQueried: "cluster-project",
		},
		{
			name:    "GSAImpersonationSameProjectSuccess",
			cluster: defaultCluster,
			ksa:     sameProjectAnnotatedKSA,
			iamReader: &fakeGCPIAMPolicyReader{
				gsaPolicy: policyWithWIFMember("serviceAccount:cluster-project.svc.id.goog[prod/app-ksa]"),
			},
			wantOutContains: []string{
				"Found IAM binding for member 'serviceAccount:cluster-project.svc.id.goog[prod/app-ksa]'",
				"All checks passed!",
			},
			wantGSAQueried: "projects/cluster-project/serviceAccounts/app-sa@cluster-project.iam.gserviceaccount.com",
		},
		{
			name:    "GSAImpersonationCrossProjectWithFleetWorkloadPoolSuccess",
			cluster: fleetCluster,
			ksa:     crossProjectAnnotatedKSA,
			iamReader: &fakeGCPIAMPolicyReader{
				gsaPolicy: policyWithWIFMember("serviceAccount:fleet-host-proj.svc.id.goog[prod/app-ksa]"),
			},
			wantOutContains: []string{
				"Found IAM binding for member 'serviceAccount:fleet-host-proj.svc.id.goog[prod/app-ksa]'",
				"All checks passed!",
			},
			wantGSAQueried: "projects/gsa-owner-proj/serviceAccounts/shared-sa@gsa-owner-proj.iam.gserviceaccount.com",
		},
		{
			name:    "GSAImpersonationMissingBindingForFleetWorkloadPool",
			cluster: fleetCluster,
			ksa:     crossProjectAnnotatedKSA,
			iamReader: &fakeGCPIAMPolicyReader{
				// Policy only grants default cluster-project pool, not the Fleet pool.
				gsaPolicy: policyWithWIFMember("serviceAccount:cluster-project.svc.id.goog[prod/app-ksa]"),
			},
			wantErrContains: []string{
				"IAM binding not found",
				"serviceAccount:fleet-host-proj.svc.id.goog[prod/app-ksa]",
			},
			wantGSAQueried: "projects/gsa-owner-proj/serviceAccounts/shared-sa@gsa-owner-proj.iam.gserviceaccount.com",
		},
		{
			name:    "GSAImpersonationCrossProjectPermissionDenied",
			cluster: fleetCluster,
			ksa:     crossProjectAnnotatedKSA,
			iamReader: &fakeGCPIAMPolicyReader{
				gsaErr: status.Error(codes.PermissionDenied, "denied"),
			},
			wantErrContains: []string{
				`cross-project owner "gsa-owner-proj" (cluster project is "cluster-project")`,
				"gcloud iam service-accounts get-iam-policy shared-sa@gsa-owner-proj.iam.gserviceaccount.com",
			},
			wantGSAQueried: "projects/gsa-owner-proj/serviceAccounts/shared-sa@gsa-owner-proj.iam.gserviceaccount.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset()
			if tc.ksa != nil {
				clientset = fake.NewSimpleClientset(tc.ksa)
			}
			var out bytes.Buffer
			checker := ksaChecker{
				projectID: "cluster-project",
				clientset: clientset,
				iam:       tc.iamReader,
				out:       &out,
			}

			err := checker.check(t.Context(), target, tc.cluster)
			if len(tc.wantErrContains) > 0 {
				if err == nil {
					t.Fatalf("checker.check(%+v, %q) = nil, want error containing %q", target, tc.cluster.Name, tc.wantErrContains)
				}
				for _, sub := range tc.wantErrContains {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("checker.check(%+v, %q) error = %q, want substring %q", target, tc.cluster.Name, err.Error(), sub)
					}
				}
			} else if err != nil {
				t.Fatalf("checker.check(%+v, %q) unexpected error: %v", target, tc.cluster.Name, err)
			}

			for _, sub := range tc.wantOutContains {
				if !strings.Contains(out.String(), sub) {
					t.Errorf("checker.check(%+v, %q) output = %q, want substring %q", target, tc.cluster.Name, out.String(), sub)
				}
			}
			if tc.iamReader.gotProject != tc.wantProjectQueried {
				t.Errorf("after checker.check(%+v, %q), queried project = %q, want %q", target, tc.cluster.Name, tc.iamReader.gotProject, tc.wantProjectQueried)
			}
			if tc.iamReader.gotGSAResource != tc.wantGSAQueried {
				t.Errorf("after checker.check(%+v, %q), queried GSA resource = %q, want %q", target, tc.cluster.Name, tc.iamReader.gotGSAResource, tc.wantGSAQueried)
			}
		})
	}
}

func TestTokenFromConfig(t *testing.T) {
	t.Run("NoAccessToken", func(t *testing.T) {
		accessToken = ""
		tokenSource := tokenFromConfig()
		assert.Nil(t, tokenSource)
	})

	t.Run("WithAccessToken", func(t *testing.T) {
		testToken := "test-access-token"
		accessToken = testToken
		defer func() { accessToken = "" }()

		tokenSource := tokenFromConfig()
		assert.NotNil(t, tokenSource)

		token, err := tokenSource.Token()
		assert.NoError(t, err)
		assert.Equal(t, testToken, token.AccessToken)
		assert.IsType(t, &oauth2.Token{}, token)
	})
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name             string
		override         string
		buildInfoVersion string
		want             string
	}{
		{
			name:             "ExplicitOverride",
			override:         "0.2.0",
			buildInfoVersion: "(devel)",
			want:             "0.2.0",
		},
		{
			name:             "ExplicitOverrideWithLeadingV",
			override:         "v1.2.3-rc.1",
			buildInfoVersion: "(devel)",
			want:             "v1.2.3-rc.1",
		},
		{
			name:             "OverrideTakesPrecedenceOverBuildInfo",
			override:         "v0.3.0",
			buildInfoVersion: "v0.2.0",
			want:             "v0.3.0",
		},
		{
			name:             "TaggedBuildInfoOverridesDefaultVersion",
			override:         defaultVersion,
			buildInfoVersion: "v0.4.0",
			want:             "v0.4.0",
		},
		{
			name:             "TaggedBuildInfoStripsBuildMetadata",
			override:         defaultVersion,
			buildInfoVersion: "v0.4.0+dirty",
			want:             "v0.4.0",
		},
		{
			name:             "UntaggedPseudoVersionFallsBackToDefaultVersion",
			override:         defaultVersion,
			buildInfoVersion: "v0.0.0-20261127043502-a1b2c3d4e5f6",
			want:             defaultVersion,
		},
		{
			name:             "PostTagPseudoVersionFallsBackToDefaultVersion",
			override:         defaultVersion,
			buildInfoVersion: "v0.1.1-0.20261127043502-a1b2c3d4e5f6+dirty",
			want:             defaultVersion,
		},
		{
			name:             "DevelBuildInfoFallsBackToDefaultVersion",
			override:         defaultVersion,
			buildInfoVersion: "(devel)",
			want:             defaultVersion,
		},
		{
			name:             "EmptyFallsBackToDefaultVersion",
			override:         "",
			buildInfoVersion: "",
			want:             defaultVersion,
		},
		{
			name:             "InvalidCharactersFallBackToDefaultVersion",
			override:         "0.1.0 bad!",
			buildInfoVersion: "",
			want:             defaultVersion,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.override, tc.buildInfoVersion); got != tc.want {
				t.Errorf("resolveVersion(%q, %q) = %q, want %q", tc.override, tc.buildInfoVersion, got, tc.want)
			}
		})
	}
}

func TestUserAgentMatchesConcordPattern(t *testing.T) {
	concordPattern := regexp.MustCompile(`^(?i)gke-wif-troubleshooter/([0-9A-Za-z._-]+)$`)
	got := userAgent()
	if got != "gke-wif-troubleshooter/"+defaultVersion {
		t.Errorf("userAgent() = %q, want %q", got, "gke-wif-troubleshooter/"+defaultVersion)
	}
	if !concordPattern.MatchString(got) {
		t.Errorf("userAgent() = %q, want match for Concord pattern %q", got, concordPattern.String())
	}
}

func TestClientOptions(t *testing.T) {
	origAccessToken := accessToken
	origInspectionToken := inspectionToken
	t.Cleanup(func() {
		accessToken = origAccessToken
		inspectionToken = origInspectionToken
	})

	tests := []struct {
		name            string
		accessToken     string
		inspectionToken string
		wantLen         int
	}{
		{
			name:    "NoTokens",
			wantLen: 1, // WithUserAgent only
		},
		{
			name:        "WithAccessToken",
			accessToken: "some-access-token",
			wantLen:     2, // WithUserAgent + WithTokenSource
		},
		{
			name:            "WithInspectionToken",
			inspectionToken: "some-inspection-token",
			wantLen:         2, // WithUserAgent + WithPerRPCCredentials
		},
		{
			name:            "WithBothTokens",
			accessToken:     "some-access-token",
			inspectionToken: "some-inspection-token",
			wantLen:         3, // WithUserAgent + WithPerRPCCredentials + WithTokenSource
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accessToken = tc.accessToken
			inspectionToken = tc.inspectionToken
			opts := clientOptions()
			if len(opts) != tc.wantLen {
				t.Errorf("len(clientOptions()) with accessToken=%t, inspectionToken=%t = %d, want %d", tc.accessToken != "", tc.inspectionToken != "", len(opts), tc.wantLen)
			}
		})
	}
}

type captureMetadataClusterServer struct {
	containerpb.UnimplementedClusterManagerServer
	mu sync.Mutex
	md metadata.MD
}

func (s *captureMetadataClusterServer) GetCluster(ctx context.Context, req *containerpb.GetClusterRequest) (*containerpb.Cluster, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.md = md.Copy()
	s.mu.Unlock()
	return &containerpb.Cluster{Name: req.GetName()}, nil
}

func (s *captureMetadataClusterServer) capturedMetadata() metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.md
}

func TestClientOptionsSendsConcordUserAgentOverGRPC(t *testing.T) {
	origAccessToken := accessToken
	origInspectionToken := inspectionToken
	origVersion := version
	t.Cleanup(func() {
		accessToken = origAccessToken
		inspectionToken = origInspectionToken
		version = origVersion
	})
	accessToken = ""
	inspectionToken = ""
	version = "1.2.3"

	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { lis.Close() })

	fakeServer := &captureMetadataClusterServer{}
	srv := grpc.NewServer()
	containerpb.RegisterClusterManagerServer(srv, fakeServer)
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.Stop()
		<-done
	})

	opts := append(clientOptions(),
		option.WithEndpoint("passthrough:///bufnet"),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithGRPCDialOption(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		})),
	)

	client, err := container.NewClusterManagerClient(t.Context(), opts...)
	if err != nil {
		t.Fatalf("container.NewClusterManagerClient() unexpected error: %v", err)
	}
	defer client.Close()

	if _, err := getGKECluster(t.Context(), client, "my-proj", "us-central1", "my-cluster"); err != nil {
		t.Fatalf("getGKECluster() unexpected error: %v", err)
	}

	md := fakeServer.capturedMetadata()
	gotUA := md.Get("user-agent")
	if len(gotUA) != 1 {
		t.Fatalf("getGKECluster() with clientOptions() (version=%q) user-agent values = %v (len %d), want 1 header", version, gotUA, len(gotUA))
	}
	wantPrefix := "gke-wif-troubleshooter/1.2.3 "
	if !strings.HasPrefix(gotUA[0], wantPrefix) {
		t.Errorf("getGKECluster() with clientOptions() (version=%q) user-agent = %q, want prefix %q", version, gotUA[0], wantPrefix)
	}
	concordPattern := regexp.MustCompile(`(?i)gke-wif-troubleshooter/([0-9A-Za-z._-]+)`)
	if matches := concordPattern.FindStringSubmatch(gotUA[0]); len(matches) < 2 || matches[1] != "1.2.3" {
		t.Errorf("getGKECluster() with clientOptions() Concord regex %q on user-agent %q = %v, want version %q", concordPattern.String(), gotUA[0], matches, "1.2.3")
	}

	gotAPIClient := md.Get("x-goog-api-client")
	if len(gotAPIClient) == 0 || !strings.Contains(gotAPIClient[0], "gl-go/") {
		t.Errorf("getGKECluster() with clientOptions() x-goog-api-client = %v, want non-empty header containing %q", gotAPIClient, "gl-go/")
	}
}

func TestInspectionTokenCredsGetRequestMetadata(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  map[string]string
	}{
		{
			name:  "EmptyToken",
			token: "",
			want:  nil,
		},
		{
			name:  "WithInspectionToken",
			token: "test-inspection-token",
			want: map[string]string{
				"x-goog-iam-authorization-token": "test-inspection-token",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			creds := inspectionTokenCreds{token: tc.token}
			got, err := creds.GetRequestMetadata(t.Context())
			if err != nil {
				t.Fatalf("inspectionTokenCreds{token: %q}.GetRequestMetadata() unexpected error: %v", tc.token, err)
			}
			if !maps.Equal(got, tc.want) {
				t.Errorf("inspectionTokenCreds{token: %q}.GetRequestMetadata() = %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}

func TestInspectionTokenCredsRequireTransportSecurity(t *testing.T) {
	creds := inspectionTokenCreds{}
	if got := creds.RequireTransportSecurity(); !got {
		t.Errorf("inspectionTokenCreds{}.RequireTransportSecurity() = %t, want true", got)
	}
}

func TestServiceAccountResource(t *testing.T) {
	tests := []struct {
		name        string
		gsaEmail    string
		wantRes     string
		wantProject string
	}{
		{
			name:        "StandardUserManagedGSA",
			gsaEmail:    "my-app@my-project.iam.gserviceaccount.com",
			wantRes:     "projects/my-project/serviceAccounts/my-app@my-project.iam.gserviceaccount.com",
			wantProject: "my-project",
		},
		{
			name:        "CrossProjectGSA",
			gsaEmail:    "shared-sa@other-project-123.iam.gserviceaccount.com",
			wantRes:     "projects/other-project-123/serviceAccounts/shared-sa@other-project-123.iam.gserviceaccount.com",
			wantProject: "other-project-123",
		},
		{
			name:        "DefaultComputeSA",
			gsaEmail:    "123456789-compute@developer.gserviceaccount.com",
			wantRes:     "projects/-/serviceAccounts/123456789-compute@developer.gserviceaccount.com",
			wantProject: "",
		},
		{
			name:        "LegacyDomainScopedProjectSA",
			gsaEmail:    "sa@my-proj.example.com.iam.gserviceaccount.com",
			wantRes:     "projects/-/serviceAccounts/sa@my-proj.example.com.iam.gserviceaccount.com",
			wantProject: "",
		},
		{
			name:        "MalformedProjectWithSlash",
			gsaEmail:    "sa@foo/bar.iam.gserviceaccount.com",
			wantRes:     "projects/-/serviceAccounts/sa@foo/bar.iam.gserviceaccount.com",
			wantProject: "",
		},
		{
			name:        "EmptyLocalPart",
			gsaEmail:    "@my-project.iam.gserviceaccount.com",
			wantRes:     "projects/-/serviceAccounts/@my-project.iam.gserviceaccount.com",
			wantProject: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, proj := serviceAccountResource(tc.gsaEmail)
			if res != tc.wantRes || proj != tc.wantProject {
				t.Errorf("serviceAccountResource(%q) = (%q, %q), want (%q, %q)", tc.gsaEmail, res, proj, tc.wantRes, tc.wantProject)
			}
		})
	}
}

func TestFormatGSAIAMPolicyError(t *testing.T) {
	legacySyntax := "serviceAccount:cluster-proj.svc.id.goog[default/my-ksa]"

	tests := []struct {
		name         string
		info         gsaIAMPolicyErrContext
		origErr      error
		wantContains []string
	}{
		{
			name: "CrossProjectPermissionDenied",
			info: gsaIAMPolicyErrContext{
				gsaEmail:       "sa@other-proj.iam.gserviceaccount.com",
				gsaProject:     "other-proj",
				clusterProject: "cluster-proj",
				legacySyntax:   legacySyntax,
			},
			origErr: status.Error(codes.PermissionDenied, "denied"),
			wantContains: []string{
				`cross-project owner "other-proj" (cluster project is "cluster-proj")`,
				"or the service account/project does not exist",
				"gcloud iam service-accounts get-iam-policy sa@other-proj.iam.gserviceaccount.com",
				legacySyntax,
			},
		},
		{
			name: "SameProjectPermissionDenied",
			info: gsaIAMPolicyErrContext{
				gsaEmail:       "sa@cluster-proj.iam.gserviceaccount.com",
				gsaProject:     "cluster-proj",
				clusterProject: "cluster-proj",
				legacySyntax:   legacySyntax,
			},
			origErr: status.Error(codes.PermissionDenied, "denied"),
			wantContains: []string{
				`on project "cluster-proj"`,
				"or the service account/project does not exist",
			},
		},
		{
			name: "WildcardProjectPermissionDenied",
			info: gsaIAMPolicyErrContext{
				gsaEmail:       "12345-compute@developer.gserviceaccount.com",
				gsaProject:     "",
				clusterProject: "cluster-proj",
				legacySyntax:   legacySyntax,
			},
			origErr: status.Error(codes.PermissionDenied, "denied"),
			wantContains: []string{
				"the GSA's owning project",
			},
		},
		{
			name: "NotFoundError",
			info: gsaIAMPolicyErrContext{
				gsaEmail:       "missing@cluster-proj.iam.gserviceaccount.com",
				gsaProject:     "cluster-proj",
				clusterProject: "cluster-proj",
				legacySyntax:   legacySyntax,
			},
			origErr: status.Error(codes.NotFound, "not found"),
			wantContains: []string{
				`failed to get IAM policy for GSA "missing@cluster-proj.iam.gserviceaccount.com" (does it exist?)`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := formatGSAIAMPolicyError(tc.info, tc.origErr)
			if !errors.Is(err, tc.origErr) {
				t.Errorf("formatGSAIAMPolicyError(%+v, %v) did not wrap original error; got %v", tc.info, tc.origErr, err)
			}
			for _, sub := range tc.wantContains {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("formatGSAIAMPolicyError(%+v, %v) = %q, want substring %q", tc.info, tc.origErr, err.Error(), sub)
				}
			}
		})
	}
}
