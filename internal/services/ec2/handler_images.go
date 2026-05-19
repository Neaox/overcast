package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── XML response types ───────────────────────────────────────────────────────

type xmlDescribeImagesResponse struct {
	XMLName   xml.Name   `xml:"DescribeImagesResponse"`
	Xmlns     string     `xml:"xmlns,attr"`
	RequestID string     `xml:"requestId"`
	ImagesSet []xmlImage `xml:"imagesSet>item"`
}

type xmlImage struct {
	ImageID            string `xml:"imageId"`
	Name               string `xml:"name"`
	Description        string `xml:"description"`
	ImageState         string `xml:"imageState"`
	ImageType          string `xml:"imageType"`
	Architecture       string `xml:"architecture"`
	RootDeviceType     string `xml:"rootDeviceType"`
	VirtualizationType string `xml:"virtualizationType"`
	IsPublic           bool   `xml:"isPublic"`
	OwnerID            string `xml:"ownerId"`
}

// syntheticAMIs is a hardcoded set of AMIs returned by DescribeImages.
var syntheticAMIs = []xmlImage{
	{
		ImageID:            "ami-12345678",
		Name:               "Amazon Linux 2",
		Description:        "Amazon Linux 2 AMI 2.0.20231218.0 x86_64 HVM gp2",
		ImageState:         "available",
		ImageType:          "machine",
		Architecture:       "x86_64",
		RootDeviceType:     "ebs",
		VirtualizationType: "hvm",
		IsPublic:           true,
		OwnerID:            "137112412989",
	},
	{
		ImageID:            "ami-0abcdef1234567890",
		Name:               "Ubuntu Server 22.04 LTS",
		Description:        "Canonical, Ubuntu, 22.04 LTS, amd64 jammy image",
		ImageState:         "available",
		ImageType:          "machine",
		Architecture:       "x86_64",
		RootDeviceType:     "ebs",
		VirtualizationType: "hvm",
		IsPublic:           true,
		OwnerID:            "099720109477",
	},
	{
		ImageID:            "ami-0fedcba987654321f",
		Name:               "Windows Server 2022 Base",
		Description:        "Microsoft Windows Server 2022 Full Locale English AMI",
		ImageState:         "available",
		ImageType:          "machine",
		Architecture:       "x86_64",
		RootDeviceType:     "ebs",
		VirtualizationType: "hvm",
		IsPublic:           true,
		OwnerID:            "801119661308",
	},
	{
		ImageID:            "ami-0aaaaaaaaaaaaaaa0",
		Name:               "Amazon Linux 2023",
		Description:        "Amazon Linux 2023 AMI 2023.3.20231218.0 x86_64 HVM kernel-6.1",
		ImageState:         "available",
		ImageType:          "machine",
		Architecture:       "x86_64",
		RootDeviceType:     "ebs",
		VirtualizationType: "hvm",
		IsPublic:           true,
		OwnerID:            "137112412989",
	},
}

// DescribeImages returns a set of synthetic AMIs, optionally filtered by ImageId.
func (h *Handler) DescribeImages(w http.ResponseWriter, r *http.Request) {
	// Collect ImageId.N filter params.
	filterIDs := parseIndexedParam(r, "ImageId")
	filterIDSet := make(map[string]bool, len(filterIDs))
	for _, id := range filterIDs {
		filterIDSet[id] = true
	}

	images := make([]xmlImage, 0, len(syntheticAMIs))
	for _, ami := range syntheticAMIs {
		if len(filterIDSet) > 0 && !filterIDSet[ami.ImageID] {
			continue
		}
		images = append(images, ami)
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeImagesResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		ImagesSet: images,
	})
}
