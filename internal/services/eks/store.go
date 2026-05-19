package eks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) clusterARN(region, name string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", region, s.accountID(), name)
}

func (s *Service) nodegroupARN(region, clusterName, nodegroupName string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:nodegroup/%s/%s/mock-ng", region, s.accountID(), clusterName, nodegroupName)
}

func (s *Service) fargateProfileARN(region, clusterName, fargateProfileName string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:fargateprofile/%s/%s/mock-fargate", region, s.accountID(), clusterName, fargateProfileName)
}

func clusterKey(region, name string) string {
	return serviceutil.RegionKey(region, name)
}

func nodegroupKey(region, clusterName, nodegroupName string) string {
	return serviceutil.RegionKey(region, clusterName+"/"+nodegroupName)
}

func updateKey(region, clusterName, updateID string) string {
	return serviceutil.RegionKey(region, clusterName+"/"+updateID)
}

func tagKey(arn string) string { return arn }

func fargateProfileKey(region, clusterName, profileName string) string {
	return serviceutil.RegionKey(region, clusterName+"/"+profileName)
}

func addonKey(region, clusterName, addonName string) string {
	return serviceutil.RegionKey(region, clusterName+"/"+addonName)
}

func idpConfigKey(region, clusterName, configType, configName string) string {
	return serviceutil.RegionKey(region, clusterName+"/"+configType+"/"+configName)
}

func accessEntryKey(region, clusterName, principalArn string) string {
	return serviceutil.RegionKey(region, clusterName+"/"+principalArn)
}

func associatedAccessPolicyKey(region, clusterName, principalArn, policyArn string) string {
	return serviceutil.RegionKey(region, clusterName+"/"+principalArn+"/"+policyArn)
}

func podIdentityAssociationKey(region, clusterName, associationID string) string {
	return serviceutil.RegionKey(region, clusterName+"/"+associationID)
}

func (s *Service) putCluster(ctx context.Context, region string, c *Cluster) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsClusters, clusterKey(region, c.Name), string(raw))
}

func (s *Service) putIdentityProviderConfig(ctx context.Context, region string, cfg *IdentityProviderConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsIDPConfigs, idpConfigKey(region, cfg.ClusterName, cfg.Type, cfg.Name), string(raw))
}

func (s *Service) listIdentityProviderConfigsForCluster(ctx context.Context, region, clusterName string) ([]*IdentityProviderConfig, error) {
	pairs, err := s.store.Scan(ctx, nsIDPConfigs, serviceutil.RegionKey(region, clusterName+"/"))
	if err != nil {
		return nil, err
	}
	out := make([]*IdentityProviderConfig, 0, len(pairs))
	for _, kv := range pairs {
		var cfg IdentityProviderConfig
		if err := json.Unmarshal([]byte(kv.Value), &cfg); err != nil {
			continue
		}
		out = append(out, &cfg)
	}
	return out, nil
}

func (s *Service) getIdentityProviderConfig(ctx context.Context, region, clusterName, configType, configName string) (*IdentityProviderConfig, bool, error) {
	raw, found, err := s.store.Get(ctx, nsIDPConfigs, idpConfigKey(region, clusterName, configType, configName))
	if err != nil || !found {
		return nil, found, err
	}
	var cfg IdentityProviderConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, false, err
	}
	return &cfg, true, nil
}

func (s *Service) getCluster(ctx context.Context, region, name string) (*Cluster, bool, error) {
	raw, found, err := s.store.Get(ctx, nsClusters, clusterKey(region, name))
	if err != nil || !found {
		return nil, found, err
	}
	var c Cluster
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, false, err
	}
	return &c, true, nil
}

func (s *Service) putAccessEntry(ctx context.Context, region string, e *AccessEntry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsAccess, accessEntryKey(region, e.ClusterName, e.PrincipalArn), string(raw))
}

func (s *Service) listAccessEntriesForCluster(ctx context.Context, region, clusterName string) ([]*AccessEntry, error) {
	pairs, err := s.store.Scan(ctx, nsAccess, serviceutil.RegionKey(region, clusterName+"/"))
	if err != nil {
		return nil, err
	}
	out := make([]*AccessEntry, 0, len(pairs))
	for _, kv := range pairs {
		var e AccessEntry
		if err := json.Unmarshal([]byte(kv.Value), &e); err != nil {
			continue
		}
		out = append(out, &e)
	}
	return out, nil
}

func (s *Service) getAccessEntry(ctx context.Context, region, clusterName, principalArn string) (*AccessEntry, bool, error) {
	raw, found, err := s.store.Get(ctx, nsAccess, accessEntryKey(region, clusterName, principalArn))
	if err != nil || !found {
		return nil, found, err
	}
	var e AccessEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return nil, false, err
	}
	return &e, true, nil
}

func (s *Service) putAssociatedAccessPolicy(ctx context.Context, region string, p *AssociatedAccessPolicy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsAccessPol, associatedAccessPolicyKey(region, p.ClusterName, p.PrincipalArn, p.PolicyArn), string(raw))
}

func (s *Service) getAssociatedAccessPolicy(ctx context.Context, region, clusterName, principalArn, policyArn string) (*AssociatedAccessPolicy, bool, error) {
	raw, found, err := s.store.Get(ctx, nsAccessPol, associatedAccessPolicyKey(region, clusterName, principalArn, policyArn))
	if err != nil || !found {
		return nil, found, err
	}
	var p AssociatedAccessPolicy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, false, err
	}
	return &p, true, nil
}

func (s *Service) listAssociatedAccessPoliciesForEntry(ctx context.Context, region, clusterName, principalArn string) ([]*AssociatedAccessPolicy, error) {
	pairs, err := s.store.Scan(ctx, nsAccessPol, serviceutil.RegionKey(region, clusterName+"/"+principalArn+"/"))
	if err != nil {
		return nil, err
	}
	out := make([]*AssociatedAccessPolicy, 0, len(pairs))
	for _, kv := range pairs {
		var p AssociatedAccessPolicy
		if err := json.Unmarshal([]byte(kv.Value), &p); err != nil {
			continue
		}
		out = append(out, &p)
	}
	return out, nil
}

func (s *Service) deleteNamespaceByPrefix(ctx context.Context, namespace, prefix string) error {
	pairs, err := s.store.Scan(ctx, namespace, prefix)
	if err != nil {
		return err
	}
	for _, kv := range pairs {
		if err := s.store.Delete(ctx, namespace, kv.Key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) podIdentityAssociationARN(region, clusterName, associationID string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:podidentityassociation/%s/%s", region, s.accountID(), clusterName, associationID)
}

func (s *Service) putPodIdentityAssociation(ctx context.Context, region string, assoc *PodIdentityAssociation) error {
	raw, err := json.Marshal(assoc)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsPodIDAssoc, podIdentityAssociationKey(region, assoc.ClusterName, assoc.AssociationID), string(raw))
}

func (s *Service) getPodIdentityAssociation(ctx context.Context, region, clusterName, associationID string) (*PodIdentityAssociation, bool, error) {
	raw, found, err := s.store.Get(ctx, nsPodIDAssoc, podIdentityAssociationKey(region, clusterName, associationID))
	if err != nil || !found {
		return nil, found, err
	}
	var assoc PodIdentityAssociation
	if err := json.Unmarshal([]byte(raw), &assoc); err != nil {
		return nil, false, err
	}
	return &assoc, true, nil
}

func (s *Service) listPodIdentityAssociationsForCluster(ctx context.Context, region, clusterName string) ([]*PodIdentityAssociation, error) {
	pairs, err := s.store.Scan(ctx, nsPodIDAssoc, serviceutil.RegionKey(region, clusterName+"/"))
	if err != nil {
		return nil, err
	}
	out := make([]*PodIdentityAssociation, 0, len(pairs))
	for _, kv := range pairs {
		var assoc PodIdentityAssociation
		if err := json.Unmarshal([]byte(kv.Value), &assoc); err != nil {
			continue
		}
		out = append(out, &assoc)
	}
	return out, nil
}

func (s *Service) putNodegroup(ctx context.Context, region string, ng *Nodegroup) error {
	raw, err := json.Marshal(ng)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsNodegroups, nodegroupKey(region, ng.ClusterName, ng.NodegroupName), string(raw))
}

func (s *Service) getNodegroup(ctx context.Context, region, clusterName, nodegroupName string) (*Nodegroup, bool, error) {
	raw, found, err := s.store.Get(ctx, nsNodegroups, nodegroupKey(region, clusterName, nodegroupName))
	if err != nil || !found {
		return nil, found, err
	}
	var ng Nodegroup
	if err := json.Unmarshal([]byte(raw), &ng); err != nil {
		return nil, false, err
	}
	return &ng, true, nil
}

func (s *Service) putUpdate(ctx context.Context, region, clusterName string, u *Update) error {
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsUpdates, updateKey(region, clusterName, u.ID), string(raw))
}

func (s *Service) getUpdate(ctx context.Context, region, clusterName, updateID string) (*Update, bool, error) {
	raw, found, err := s.store.Get(ctx, nsUpdates, updateKey(region, clusterName, updateID))
	if err != nil || !found {
		return nil, found, err
	}
	var u Update
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		return nil, false, err
	}
	return &u, true, nil
}

func (s *Service) listNodegroupsForCluster(ctx context.Context, region, clusterName string) ([]*Nodegroup, error) {
	pairs, err := s.store.Scan(ctx, nsNodegroups, serviceutil.RegionKey(region, clusterName+"/"))
	if err != nil {
		return nil, err
	}
	out := make([]*Nodegroup, 0, len(pairs))
	for _, kv := range pairs {
		var ng Nodegroup
		if err := json.Unmarshal([]byte(kv.Value), &ng); err != nil {
			continue
		}
		out = append(out, &ng)
	}
	return out, nil
}

func (s *Service) putFargateProfile(ctx context.Context, region string, fp *FargateProfile) error {
	raw, err := json.Marshal(fp)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsFargate, fargateProfileKey(region, fp.ClusterName, fp.FargateProfileName), string(raw))
}

func (s *Service) getFargateProfile(ctx context.Context, region, clusterName, profileName string) (*FargateProfile, bool, error) {
	raw, found, err := s.store.Get(ctx, nsFargate, fargateProfileKey(region, clusterName, profileName))
	if err != nil || !found {
		return nil, found, err
	}
	var fp FargateProfile
	if err := json.Unmarshal([]byte(raw), &fp); err != nil {
		return nil, false, err
	}
	return &fp, true, nil
}

func (s *Service) listFargateProfilesForCluster(ctx context.Context, region, clusterName string) ([]*FargateProfile, error) {
	pairs, err := s.store.Scan(ctx, nsFargate, serviceutil.RegionKey(region, clusterName+"/"))
	if err != nil {
		return nil, err
	}
	out := make([]*FargateProfile, 0, len(pairs))
	for _, kv := range pairs {
		var fp FargateProfile
		if err := json.Unmarshal([]byte(kv.Value), &fp); err != nil {
			continue
		}
		out = append(out, &fp)
	}
	return out, nil
}

func (s *Service) addonARN(region, clusterName, addonName string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:addon/%s/%s/mock-addon", region, s.accountID(), clusterName, addonName)
}

func (s *Service) putAddon(ctx context.Context, region string, a *Addon) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsAddons, addonKey(region, a.ClusterName, a.AddonName), string(raw))
}

func (s *Service) getAddon(ctx context.Context, region, clusterName, addonName string) (*Addon, bool, error) {
	raw, found, err := s.store.Get(ctx, nsAddons, addonKey(region, clusterName, addonName))
	if err != nil || !found {
		return nil, found, err
	}
	var a Addon
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, false, err
	}
	return &a, true, nil
}

func (s *Service) listAddonsForCluster(ctx context.Context, region, clusterName string) ([]*Addon, error) {
	pairs, err := s.store.Scan(ctx, nsAddons, serviceutil.RegionKey(region, clusterName+"/"))
	if err != nil {
		return nil, err
	}
	out := make([]*Addon, 0, len(pairs))
	for _, kv := range pairs {
		var a Addon
		if err := json.Unmarshal([]byte(kv.Value), &a); err != nil {
			continue
		}
		out = append(out, &a)
	}
	return out, nil
}

func (s *Service) identityProviderConfigARN(region, clusterName, configType, configName string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:identityproviderconfig/%s/%s/%s", region, s.accountID(), clusterName, configType, configName)
}

func (s *Service) accessEntryARN(region, clusterName, principalArn string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:access-entry/%s/%s", region, s.accountID(), clusterName, url.PathEscape(principalArn))
}
