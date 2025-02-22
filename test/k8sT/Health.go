// SPDX-License-Identifier: Apache-2.0
// Copyright 2018-2019 Authors of Cilium

package k8sTest

import (
	"context"
	"fmt"

	. "github.com/cilium/cilium/test/ginkgo-ext"
	"github.com/cilium/cilium/test/helpers"

	. "github.com/onsi/gomega"
)

var _ = Describe("K8sHealthTest", func() {
	SkipContextIf(func() bool {
		return helpers.DoesNotRunOnGKE() && helpers.DoesNotRunOnEKS()
	}, "cilium-health", func() {
		var (
			kubectl        *helpers.Kubectl
			ciliumFilename string
		)

		BeforeAll(func() {
			kubectl = helpers.CreateKubectl(helpers.K8s1VMName(), logger)

			ciliumFilename = helpers.TimestampFilename("cilium.yaml")
			DeployCiliumOptionsAndDNS(kubectl, ciliumFilename, map[string]string{
				"clusterHealthPort": "9940", // tests use of custom port
			})
		})

		AfterFailed(func() {
			kubectl.CiliumReport("cilium endpoint list")
		})

		JustAfterEach(func() {
			kubectl.ValidateNoErrorsInLogs(CurrentGinkgoTestDescription().Duration)
		})

		AfterEach(func() {
			ExpectAllPodsTerminated(kubectl)
		})

		AfterAll(func() {
			UninstallCiliumFromManifest(kubectl, ciliumFilename)
			kubectl.CloseSSHClient()
		})

		getCilium := func(node string) (pod, ip string) {
			pod, err := kubectl.GetCiliumPodOnNode(node)
			Expect(err).Should(BeNil())

			res, err := kubectl.Get(
				helpers.CiliumNamespace,
				fmt.Sprintf("pod %s", pod)).Filter("{.status.podIP}")
			Expect(err).Should(BeNil())
			ip = res.String()

			return pod, ip
		}

		checkIP := func(pod, ip string) {
			jsonpath := "{.nodes[*].host.primary-address.ip}"
			ciliumCmd := fmt.Sprintf("cilium-health status -o jsonpath='%s'", jsonpath)

			err := kubectl.CiliumExecUntilMatch(pod, ciliumCmd, ip)
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Never saw cilium-health ip %s in pod %s", ip, pod)
		}

		It("Checks status between nodes", func() {
			cilium1, cilium1IP := getCilium(helpers.K8s1)
			cilium2, cilium2IP := getCilium(helpers.K8s2)

			By("checking that cilium API exposes health instances")
			checkIP(cilium1, cilium1IP)
			checkIP(cilium1, cilium2IP)
			checkIP(cilium2, cilium1IP)
			checkIP(cilium2, cilium2IP)

			By("checking that `cilium-health --probe` succeeds")
			healthCmd := "cilium-health status --probe -o json"
			status := kubectl.CiliumExecMustSucceed(context.TODO(), cilium1, healthCmd)
			Expect(status.Stdout()).ShouldNot(ContainSubstring("error"))

			apiPaths := []string{
				"health-endpoint.primary-address.icmp",
				"health-endpoint.primary-address.http",
				"host.primary-address.icmp",
				"host.primary-address.http",
			}
			for node := 0; node <= 1; node++ {
				healthCmd := "cilium-health status -o json"
				status := kubectl.CiliumExecMustSucceed(context.TODO(), cilium1, healthCmd, "Cannot retrieve health status")
				for _, path := range apiPaths {
					filter := fmt.Sprintf("{.nodes[%d].%s}", node, path)
					By("checking API response for %q", filter)
					data, err := status.Filter(filter)
					Expect(err).To(BeNil(), "cannot retrieve filter %q from health output", filter)
					Expect(data.String()).Should(Not((BeEmpty())))
					statusFilter := fmt.Sprintf("{.nodes[%d].%s.status}", node, path)
					By("checking API status response for %q", statusFilter)
					data, err = status.Filter(statusFilter)
					Expect(err).To(BeNil(), "cannot retrieve filter %q from health output", statusFilter)
					Expect(data.String()).Should(BeEmpty())
				}
			}
		}, 30)
	})
})
