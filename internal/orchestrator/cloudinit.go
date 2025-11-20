package orchestrator

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"text/template"
)

// Путь к шаблону
const TemplatePath = "configs/user-data.yaml"

func GenerateCloudInitISO(instanceDir string, pubKey string, instanceIP string) (string, error) {
	userDataPath := fmt.Sprintf("%s/user-data", instanceDir)
	metaDataPath := fmt.Sprintf("%s/meta-data", instanceDir)
	isoPath := fmt.Sprintf("%s/cloud-init.disk", instanceDir)

	// 1. Читаем шаблон и вставляем ключ
	tmpl, err := template.ParseFiles(TemplatePath)
	if err != nil {
		return "", fmt.Errorf("failed to load template %s: %v", TemplatePath, err)
	}

	var userData bytes.Buffer
	if err := tmpl.Execute(&userData, map[string]string{"SSHKey": pubKey}); err != nil {
		return "", err
	}

	metaData := fmt.Sprintf(
		`instance-id: i-%s
				local-hostname: microvm
				network:
				  version: 2
				  ethernets:
					eth0:
					  addresses:
						- %s/24
					  gateway4: 172.16.0.1
					  nameservers:
						addresses: [8.8.8.8]
				`, "vm-id", instanceIP)

	if err := os.WriteFile(userDataPath, userData.Bytes(), 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(metaDataPath, []byte(metaData), 0644); err != nil {
		return "", err
	}
	
	cmd := exec.Command("cloud-localds", isoPath, userDataPath, metaDataPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("iso gen failed: %s, %v", string(output), err)
	}

	return isoPath, nil
}
