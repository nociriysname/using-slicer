package orchestrator

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"text/template"
)

const TemplatePath = "configs/user-data.yaml"

func GenerateCloudInitISO(instanceDir string, pubKey string, instanceIP string) (string, error) {
	userDataPath := fmt.Sprintf("%s/user-data", instanceDir)
	metaDataPath := fmt.Sprintf("%s/meta-data", instanceDir)
	isoPath := fmt.Sprintf("%s/cloud-init.disk", instanceDir)

	tmpl, err := template.ParseFiles(TemplatePath)
	if err != nil {
		return "", fmt.Errorf("template error: %v", err)
	}

	var userData bytes.Buffer
	if err := tmpl.Execute(&userData, map[string]string{"SSHKey": pubKey}); err != nil {
		return "", err
	}

	metaData := fmt.Sprintf(`instance-id: i-%s
									local-hostname: microvm
									`, "vm-id")

	if err := os.WriteFile(userDataPath, userData.Bytes(), 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(metaDataPath, []byte(metaData), 0644); err != nil {
		return "", err
	}

	cmd := exec.Command("genisoimage",
		"-output", isoPath,
		"-volid", "cidata",
		"-joliet", "-rock",
		userDataPath, metaDataPath)

	if output, err := cmd.CombinedOutput(); err != nil {
		cmdfallback := exec.Command("cloud-localds", isoPath, userDataPath, metaDataPath)
		if out2, err2 := cmdfallback.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("iso gen failed: %s / %s", string(output), string(out2))
		}
	}

	return isoPath, nil
}
