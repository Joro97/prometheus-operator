package controller

import (
	"fmt"

	"github.com/coreos/kube-prometheus-controller/pkg/spec"
	"k8s.io/client-go/1.5/pkg/api/v1"
	"k8s.io/client-go/1.5/pkg/apis/apps/v1alpha1"
	"k8s.io/client-go/1.5/pkg/util/intstr"
)

func makePetSet(p *spec.Prometheus, old *v1alpha1.PetSet) *v1alpha1.PetSet {
	// TODO(fabxc): is this the right point to inject defaults?
	// Ideally we would do it before storing but that's currently not possible.
	// Potentially an update handler on first insertion.

	baseImage := p.Spec.BaseImage
	if baseImage == "" {
		baseImage = "quay.io/prometheus/prometheus"
	}
	version := p.Spec.Version
	if version == "" {
		version = "v1.3.0-beta.0"
	}
	replicas := p.Spec.Replicas
	if replicas < 1 {
		replicas = 1
	}
	image := fmt.Sprintf("%s:%s", baseImage, version)

	petset := &v1alpha1.PetSet{
		ObjectMeta: v1.ObjectMeta{
			Name: p.Name,
		},
		Spec: makePetSetSpec(p.Name, image, replicas),
	}
	if vc := p.Spec.Storage; vc == nil {
		petset.Spec.Template.Spec.Volumes = append(petset.Spec.Template.Spec.Volumes, v1.Volume{
			Name: fmt.Sprintf("%s-db", p.Name),
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{},
			},
		})
	} else {
		pvc := v1.PersistentVolumeClaim{
			ObjectMeta: v1.ObjectMeta{
				Name: fmt.Sprintf("%s-db", p.Name),
				Annotations: map[string]string{
					"volume.alpha.kubernetes.io/storage-class": vc.Class,
				},
			},
			Spec: v1.PersistentVolumeClaimSpec{
				AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
				Resources:   vc.Resources,
				Selector:    vc.Selector,
			},
		}
		petset.Spec.VolumeClaimTemplates = append(petset.Spec.VolumeClaimTemplates, pvc)
	}

	if old != nil {
		petset.Annotations = old.Annotations
	}
	return petset
}

func makePetSetService(p *spec.Prometheus) *v1.Service {
	svc := &v1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name: fmt.Sprintf("%s-petset", p.Name),
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"prometheus.coreos.com/name": p.Name,
				"prometheus.coreos.com/type": "prometheus",
			},
			ClusterIP: "None",
			Ports: []v1.ServicePort{
				{
					Name:       "web",
					Port:       9090,
					TargetPort: intstr.FromString("web"),
				},
			},
		},
	}
	return svc
}

func makePetSetSpec(name, image string, replicas int32) v1alpha1.PetSetSpec {
	// Prometheus may take quite long to shut down to checkpoint existing data.
	// Allow up to 10 minutes for clean termination.
	terminationGracePeriod := int64(600)

	return v1alpha1.PetSetSpec{
		ServiceName: fmt.Sprintf("%s-petset", name),
		Replicas:    &replicas,
		Template: v1.PodTemplateSpec{
			ObjectMeta: v1.ObjectMeta{
				Labels: map[string]string{
					"prometheus.coreos.com/name": name,
					"prometheus.coreos.com/type": "prometheus",
				},
				Annotations: map[string]string{
					"pod.alpha.kubernetes.io/initialized": "true",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "prometheus",
						Image: image,
						Ports: []v1.ContainerPort{
							{
								Name:          "web",
								ContainerPort: 9090,
								Protocol:      v1.ProtocolTCP,
							},
						},
						Args: []string{
							"-storage.local.retention=12h",
							"-storage.local.memory-chunks=500000",
							"-storage.local.path=/var/prometheus/data",
							"-config.file=/etc/prometheus/config/prometheus.yaml",
						},
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "config",
								ReadOnly:  true,
								MountPath: "/etc/prometheus/config",
							},
							{
								Name:      "rules",
								ReadOnly:  true,
								MountPath: "/etc/prometheus/rules",
							},
							{
								Name:      fmt.Sprintf("%s-db", name),
								MountPath: "/var/prometheus/data",
							},
						},
						ReadinessProbe: &v1.Probe{
							Handler: v1.Handler{
								HTTPGet: &v1.HTTPGetAction{
									Path: "/status",
									Port: intstr.FromString("web"),
								},
							},
							InitialDelaySeconds: 1,
							TimeoutSeconds:      3,
							PeriodSeconds:       5,
							// For larger servers, restoring a checkpoint on startup may take quite a bit of time.
							// Wait up to 5 minutes.
							FailureThreshold: 100,
						},
					}, {
						Name:  "config-reloader",
						Image: "jimmidyson/configmap-reload",
						Args: []string{
							"-webhook-url=http://localhost:9090/-/reload",
							"-volume-dir=/etc/prometheus/config",
						},
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "config",
								ReadOnly:  true,
								MountPath: "/etc/prometheus/config",
							},
						},
					}, {
						Name:  "rules-reloader",
						Image: "jimmidyson/configmap-reload",
						Args: []string{
							"-webhook-url=http://localhost:9090/-/reload",
							"-volume-dir=/etc/prometheus/rules/",
						},
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "rules",
								ReadOnly:  true,
								MountPath: "/etc/prometheus/rules",
							},
						},
					},
				},
				TerminationGracePeriodSeconds: &terminationGracePeriod,
				Volumes: []v1.Volume{
					{
						Name: "config",
						VolumeSource: v1.VolumeSource{
							ConfigMap: &v1.ConfigMapVolumeSource{
								LocalObjectReference: v1.LocalObjectReference{
									Name: name,
								},
							},
						},
					},
					{
						Name: "rules",
						VolumeSource: v1.VolumeSource{
							ConfigMap: &v1.ConfigMapVolumeSource{
								LocalObjectReference: v1.LocalObjectReference{
									Name: fmt.Sprintf("%s-rules", name),
								},
							},
						},
					},
				},
			},
		},
	}
}
