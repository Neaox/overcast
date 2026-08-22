package ec2

import (
	"context"
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

// DescribeImages returns the synthetic AMIs that match the request.
func (h *Handler) DescribeImages(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeImages(r.Context(), requestQuery(r, "ImageId"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeImages(ctx context.Context, q describeQuery) (*xmlDescribeImagesResponse, *protocol.AWSError) {
	filters, aerr := imageFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}
	requested := q.ids

	images := make([]xmlImage, 0, len(syntheticAMIs))
	for _, ami := range syntheticAMIs {
		if !requested.has(ami.ImageID) || !filters.matches(ami) {
			continue
		}
		images = append(images, ami)
	}

	return &xmlDescribeImagesResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		ImagesSet: images,
	}, nil
}
