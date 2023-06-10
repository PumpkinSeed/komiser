package compute

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/tailwarden/komiser/models"
	"github.com/tailwarden/komiser/providers"
	"github.com/tailwarden/komiser/utils"
	"github.com/tailwarden/komiser/utils/gcpcomputepricing"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
)

func Disks(ctx context.Context, client providers.ProviderClient) ([]models.Resource, error) {
	resources := make([]models.Resource, 0)

	disksClient, err := compute.NewDisksRESTClient(ctx, option.WithCredentials(client.GCPClient.Credentials))
	if err != nil {
		logrus.WithError(err).Errorf("failed to create compute client")
		return resources, err
	}

	req := &computepb.AggregatedListDisksRequest{
		Project: client.GCPClient.Credentials.ProjectID,
	}
	disks := disksClient.AggregatedList(ctx, req)

	actualPricing, err := gcpcomputepricing.Fetch()
	if err != nil {
		logrus.WithError(err).Errorf("failed to fetch actual GCP VM pricing")
		return resources, err
	}

	for {
		disksListPair, err := disks.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			logrus.WithError(err).Errorf("failed to list instances")
			return resources, err
		}
		if len(disksListPair.Value.Disks) == 0 {
			continue
		}

		for _, disk := range disksListPair.Value.Disks {
			tags := make([]models.Tag, 0)
			if disk.Labels != nil {
				for key, value := range disk.Labels {
					tags = append(tags, models.Tag{
						Key:   key,
						Value: value,
					})
				}
			}

			zone := utils.GcpExtractZoneFromURL(disk.GetZone())

			cost, err := calculateDiskCost(ctx, client, calculateDiskCostData{
				sizeGb:  disk.SizeGb,
				typ:     disk.Type,
				pricing: actualPricing,
				zone:    zone,
			})
			if err != nil {
				logrus.WithError(err).Errorf("failed calculate disk cost")
				return resources, err
			}

			resources = append(resources, models.Resource{
				Provider:   "GCP",
				Account:    client.Name,
				Service:    "Compute Disk",
				ResourceId: fmt.Sprintf("%d", disk.GetId()),
				Region:     zone,
				Name:       disk.GetName(),
				FetchedAt:  time.Now(),
				Cost:       cost,
				Tags:       tags,
				Link:       fmt.Sprintf("https://console.cloud.google.com/compute/disksDetail/zones/%s/disks/%s?project=%s", zone, disk.GetName(), client.GCPClient.Credentials.ProjectID),
			})
		}
	}

	logrus.WithFields(logrus.Fields{
		"provider":  "GCP",
		"account":   client.Name,
		"service":   "Compute Engine",
		"resources": len(resources),
	}).Info("Fetched resources")

	return resources, nil
}

// Balanced: Storagepdssdlitecapacity
// Balanced regional: Storageregionalpdssdlitecapacity

type calculateDiskCostData struct {
	sizeGb   *int64
	typ      *string
	regional bool
	zone     string
	pricing  *gcpcomputepricing.Pricing
}

func calculateDiskCost(ctx context.Context, client providers.ProviderClient, data calculateDiskCostData) (float64, error) {
	if data.typ == nil {
		logrus.Errorf("disk type is missing from gcp calculation")
		return 0, nil
	}
	if data.sizeGb == nil {
		logrus.Errorf("disk size is missing from gcp calculation")
		return 0, nil
	}
	var typ = *data.typ
	var sizeGB = *data.sizeGb

	switch {
	case strings.Contains(typ, "pd-ssd"):
	case strings.Contains(typ, "pd-balanced"):
		if data.regional {
			prices := data.pricing.Gcp.Compute.PersistentDisk.SSD.Capacity.Lite.Storagepdssdlitecapacity.Regions[utils.GcpGetRegionFromZone(data.zone)].Prices
			fmt.Println(prices)
		} else {
			prices := data.pricing.Gcp.Compute.PersistentDisk.SSD.Capacity.Lite.Storagepdssdlitecapacity.Regions[utils.GcpGetRegionFromZone(data.zone)].Prices
			monthlyRate := float64(prices[0].Nanos) / 1000000000
			return monthlyRate * float64(sizeGB), nil
		}
	case strings.Contains(typ, "pd-standard"):
	case strings.Contains(typ, "pd-extreme"):
	}
	return 0, nil
}
