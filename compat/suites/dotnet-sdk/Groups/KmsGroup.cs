using System.Text;
using Amazon.KeyManagementService;
using Amazon.KeyManagementService.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class KmsGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["CreateKey"] = CreateKeyAsync,
        ["DescribeKey"] = DescribeKeyAsync,
        ["CreateKmsAlias"] = CreateKmsAliasAsync,
        ["ListKmsAliases"] = ListKmsAliasesAsync,
        ["ListKeys"] = ListKeysAsync,
        ["DisableKey"] = DisableKeyAsync,
        ["EnableKey"] = EnableKeyAsync,
        ["ScheduleKeyDeletion"] = ScheduleKeyDeletionAsync,
        ["CancelKeyDeletion"] = CancelKeyDeletionAsync,
        ["TagKMSResource"] = TagKMSResourceAsync,
        ["ListKMSResourceTags"] = ListKMSResourceTagsAsync,
        ["UntagKMSResource"] = UntagKMSResourceAsync,
        ["Encrypt"] = EncryptAsync,
        ["Decrypt"] = DecryptAsync,
        ["GenerateDataKey"] = GenerateDataKeyAsync,
        ["GenerateDataKeyWithoutPlaintext"] = GenerateDataKeyWithoutPlaintextAsync,
        ["Sign"] = SignAsync,
        ["Verify"] = VerifyAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["kms-keys"] = SetupKeysAsync,
        ["kms-crypto"] = SetupCryptoAsync,
        ["kms-asymmetric"] = SetupAsymmetricAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["kms-keys"] = TeardownKeysAsync,
        ["kms-crypto"] = TeardownCryptoAsync,
        ["kms-asymmetric"] = TeardownAsymmetricAsync,
    };

    // ── kms-keys ──

    private async Task SetupKeysAsync(TestContext context)
    {
        var response = await clients.KMS().CreateKeyAsync(new CreateKeyRequest());
        context.Set("KmsKeyId", response.KeyMetadata.KeyId);
    }

    private async Task CreateKeyAsync(TestContext context)
    {
        var response = await clients.KMS().CreateKeyAsync(new CreateKeyRequest());
        var keyId = response.KeyMetadata.KeyId;
        Assertions.NotBlank(keyId, "CreateKey: KeyId");
        Assertions.NotBlank(response.KeyMetadata.Arn, "CreateKey: Arn");
        try
        {
            var list = await clients.KMS().ListKeysAsync(new ListKeysRequest());
            Assertions.True(list.Keys.Any(k => k.KeyId == keyId), $"CreateKey: key {keyId} not found in ListKeys (runId={context.RunId})");
        }
        finally
        {
            try { await clients.KMS().ScheduleKeyDeletionAsync(new ScheduleKeyDeletionRequest { KeyId = keyId, PendingWindowInDays = 7 }); } catch { }
        }
    }

    private async Task DescribeKeyAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        var response = await clients.KMS().DescribeKeyAsync(new DescribeKeyRequest { KeyId = keyId });
        Assertions.Equal(keyId, response.KeyMetadata.KeyId, "DescribeKey: KeyId mismatch");
        Assertions.NotBlank(response.KeyMetadata.Arn, "DescribeKey: Arn");
    }

    private async Task CreateKmsAliasAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        var aliasName = $"alias/{context.RunId}-compat-key";
        await clients.KMS().CreateAliasAsync(new CreateAliasRequest { AliasName = aliasName, TargetKeyId = keyId });
        try
        {
            var aliases = await clients.KMS().ListAliasesAsync(new ListAliasesRequest { KeyId = keyId });
            Assertions.True(aliases.Aliases.Any(a => a.AliasName == aliasName), $"CreateKmsAlias: alias {aliasName} not found (runId={context.RunId})");
        }
        finally
        {
            try { await clients.KMS().DeleteAliasAsync(new DeleteAliasRequest { AliasName = aliasName }); } catch { }
        }
    }

    private async Task ListKmsAliasesAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        var aliasName = $"alias/{context.RunId}-list-aliases";
        await clients.KMS().CreateAliasAsync(new CreateAliasRequest { AliasName = aliasName, TargetKeyId = keyId });
        try
        {
            var aliases = await clients.KMS().ListAliasesAsync(new ListAliasesRequest { KeyId = keyId });
            Assertions.True(aliases.Aliases.Any(a => a.AliasName == aliasName), $"ListKmsAliases: alias {aliasName} not found (runId={context.RunId})");
            Assertions.NotBlank(aliases.Aliases[0].AliasName, "ListKmsAliases: AliasName");
        }
        finally
        {
            try { await clients.KMS().DeleteAliasAsync(new DeleteAliasRequest { AliasName = aliasName }); } catch { }
        }
    }

    private async Task ListKeysAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        var list = await clients.KMS().ListKeysAsync(new ListKeysRequest());
        Assertions.True(list.Keys.Count > 0, "ListKeys: expected non-empty keys");
        Assertions.True(list.Keys.Any(k => k.KeyId == keyId), $"ListKeys: key {keyId} not found (runId={context.RunId})");
    }

    private async Task DisableKeyAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        await clients.KMS().DisableKeyAsync(new DisableKeyRequest { KeyId = keyId });
        var desc = await clients.KMS().DescribeKeyAsync(new DescribeKeyRequest { KeyId = keyId });
        Assertions.Equal(KeyState.Disabled, desc.KeyMetadata.KeyState, "DisableKey: KeyState");
        await clients.KMS().EnableKeyAsync(new EnableKeyRequest { KeyId = keyId });
    }

    private async Task EnableKeyAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        await clients.KMS().EnableKeyAsync(new EnableKeyRequest { KeyId = keyId });
        var desc = await clients.KMS().DescribeKeyAsync(new DescribeKeyRequest { KeyId = keyId });
        Assertions.Equal(KeyState.Enabled, desc.KeyMetadata.KeyState, "EnableKey: KeyState should be Enabled");
    }

    private async Task ScheduleKeyDeletionAsync(TestContext context)
    {
        var createResp = await clients.KMS().CreateKeyAsync(new CreateKeyRequest());
        var keyId = createResp.KeyMetadata.KeyId;
        var schedResp = await clients.KMS().ScheduleKeyDeletionAsync(new ScheduleKeyDeletionRequest { KeyId = keyId, PendingWindowInDays = 7 });
        Assertions.NotBlank(schedResp.KeyId, "ScheduleKeyDeletion: KeyId");
        var desc = await clients.KMS().DescribeKeyAsync(new DescribeKeyRequest { KeyId = keyId });
        Assertions.Equal(KeyState.PendingDeletion, desc.KeyMetadata.KeyState, "ScheduleKeyDeletion: KeyState");
    }

    private async Task CancelKeyDeletionAsync(TestContext context)
    {
        var createResp = await clients.KMS().CreateKeyAsync(new CreateKeyRequest());
        var keyId = createResp.KeyMetadata.KeyId;
        try
        {
            await clients.KMS().ScheduleKeyDeletionAsync(new ScheduleKeyDeletionRequest { KeyId = keyId, PendingWindowInDays = 7 });
            await clients.KMS().CancelKeyDeletionAsync(new CancelKeyDeletionRequest { KeyId = keyId });
            var desc = await clients.KMS().DescribeKeyAsync(new DescribeKeyRequest { KeyId = keyId });
            Assertions.Equal(KeyState.Disabled, desc.KeyMetadata.KeyState, "CancelKeyDeletion: KeyState");
        }
        finally
        {
            try { await clients.KMS().ScheduleKeyDeletionAsync(new ScheduleKeyDeletionRequest { KeyId = keyId, PendingWindowInDays = 7 }); } catch { }
        }
    }

    private async Task TagKMSResourceAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        await clients.KMS().TagResourceAsync(new TagResourceRequest
        {
            KeyId = keyId,
            Tags = [new Tag { TagKey = "project", TagValue = "overcast" }],
        });
        var tags = await clients.KMS().ListResourceTagsAsync(new ListResourceTagsRequest { KeyId = keyId });
        Assertions.True(tags.Tags.Any(t => t.TagKey == "project"), "TagKMSResource: 'project' tag not found");
        Assertions.Equal("overcast", tags.Tags.First(t => t.TagKey == "project").TagValue, "TagKMSResource: tag value mismatch");
    }

    private async Task ListKMSResourceTagsAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        var tags = await clients.KMS().ListResourceTagsAsync(new ListResourceTagsRequest { KeyId = keyId });
        Assertions.True(tags.Tags.Any(t => t.TagKey == "project"), "ListKMSResourceTags: 'project' tag not found");
    }

    private async Task UntagKMSResourceAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsKeyId");
        await clients.KMS().UntagResourceAsync(new UntagResourceRequest
        {
            KeyId = keyId,
            TagKeys = ["project"],
        });
        var tags = await clients.KMS().ListResourceTagsAsync(new ListResourceTagsRequest { KeyId = keyId });
        Assertions.False(tags.Tags.Any(t => t.TagKey == "project"), "UntagKMSResource: 'project' tag still present");
    }

    private async Task TeardownKeysAsync(TestContext context)
    {
        var keyId = context.GetString("KmsKeyId");
        if (!string.IsNullOrWhiteSpace(keyId))
        {
            try
            {
                var aliases = await clients.KMS().ListAliasesAsync(new ListAliasesRequest { KeyId = keyId });
                foreach (var alias in aliases.Aliases)
                {
                    try { await clients.KMS().DeleteAliasAsync(new DeleteAliasRequest { AliasName = alias.AliasName }); } catch { }
                }
            }
            catch { }
            try { await clients.KMS().ScheduleKeyDeletionAsync(new ScheduleKeyDeletionRequest { KeyId = keyId, PendingWindowInDays = 7 }); } catch { }
        }
    }

    // ── kms-crypto ──

    private async Task SetupCryptoAsync(TestContext context)
    {
        var response = await clients.KMS().CreateKeyAsync(new CreateKeyRequest());
        context.Set("KmsCryptoKeyId", response.KeyMetadata.KeyId);
    }

    private async Task EncryptAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsCryptoKeyId");
        using var plaintext = new MemoryStream(Encoding.UTF8.GetBytes("hello"));
        var response = await clients.KMS().EncryptAsync(new EncryptRequest { KeyId = keyId, Plaintext = plaintext });
        Assertions.GreaterThan(0, response.CiphertextBlob.Length, "Encrypt: CiphertextBlob");
        Assertions.NotBlank(response.KeyId, "Encrypt: KeyId");
    }

    private async Task DecryptAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsCryptoKeyId");
        using var plaintext = new MemoryStream(Encoding.UTF8.GetBytes("hello overcast"));
        var encryptResponse = await clients.KMS().EncryptAsync(new EncryptRequest { KeyId = keyId, Plaintext = plaintext });
        var decryptResponse = await clients.KMS().DecryptAsync(new DecryptRequest
        {
            KeyId = keyId,
            CiphertextBlob = encryptResponse.CiphertextBlob,
        });
        using var reader = new StreamReader(decryptResponse.Plaintext);
        var decrypted = await reader.ReadToEndAsync();
        Assertions.Equal("hello overcast", decrypted, "Decrypt: round trip mismatch");
    }

    private async Task GenerateDataKeyAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsCryptoKeyId");
        var response = await clients.KMS().GenerateDataKeyAsync(new GenerateDataKeyRequest
        {
            KeyId = keyId,
            KeySpec = DataKeySpec.AES_256,
        });
        Assertions.GreaterThan(0, response.CiphertextBlob.Length, "GenerateDataKey: CiphertextBlob");
        Assertions.GreaterThan(0, response.Plaintext.Length, "GenerateDataKey: Plaintext");
        Assertions.NotBlank(response.KeyId, "GenerateDataKey: KeyId");
    }

    private async Task GenerateDataKeyWithoutPlaintextAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsCryptoKeyId");
        var response = await clients.KMS().GenerateDataKeyWithoutPlaintextAsync(new GenerateDataKeyWithoutPlaintextRequest
        {
            KeyId = keyId,
            KeySpec = DataKeySpec.AES_256,
        });
        Assertions.GreaterThan(0, response.CiphertextBlob.Length, "GenerateDataKeyWithoutPlaintext: CiphertextBlob");
        Assertions.NotBlank(response.KeyId, "GenerateDataKeyWithoutPlaintext: KeyId");
    }

    private async Task TeardownCryptoAsync(TestContext context)
    {
        var keyId = context.GetString("KmsCryptoKeyId");
        if (!string.IsNullOrWhiteSpace(keyId))
        {
            try { await clients.KMS().ScheduleKeyDeletionAsync(new ScheduleKeyDeletionRequest { KeyId = keyId, PendingWindowInDays = 7 }); } catch { }
        }
    }

    // ── kms-asymmetric ──

    private async Task SetupAsymmetricAsync(TestContext context)
    {
        var response = await clients.KMS().CreateKeyAsync(new CreateKeyRequest
        {
            KeySpec = "RSA_2048",
            KeyUsage = KeyUsageType.SIGN_VERIFY,
        });
        context.Set("KmsAsymmetricKeyId", response.KeyMetadata.KeyId);
    }

    private async Task SignAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsAsymmetricKeyId");
        using var message = new MemoryStream(Encoding.UTF8.GetBytes("hello"));
        var response = await clients.KMS().SignAsync(new SignRequest
        {
            KeyId = keyId,
            Message = message,
            SigningAlgorithm = SigningAlgorithmSpec.RSASSA_PKCS1_V1_5_SHA_256,
        });
        Assertions.GreaterThan(0, response.Signature.Length, "Sign: signature");
        Assertions.NotBlank(response.KeyId, "Sign: KeyId");
    }

    private async Task VerifyAsync(TestContext context)
    {
        var keyId = RequireKeyId(context, "KmsAsymmetricKeyId");
        using var message = new MemoryStream(Encoding.UTF8.GetBytes("hello"));
        var signResponse = await clients.KMS().SignAsync(new SignRequest
        {
            KeyId = keyId,
            Message = message,
            SigningAlgorithm = SigningAlgorithmSpec.RSASSA_PKCS1_V1_5_SHA_256,
        });
        using var verifyMessage = new MemoryStream(Encoding.UTF8.GetBytes("hello"));
        var verifyResponse = await clients.KMS().VerifyAsync(new VerifyRequest
        {
            KeyId = keyId,
            Message = verifyMessage,
            Signature = signResponse.Signature,
            SigningAlgorithm = SigningAlgorithmSpec.RSASSA_PKCS1_V1_5_SHA_256,
        });
        Assertions.True(verifyResponse.SignatureValid ?? false, "Verify: SignatureValid");
    }

    private async Task TeardownAsymmetricAsync(TestContext context)
    {
        var keyId = context.GetString("KmsAsymmetricKeyId");
        if (!string.IsNullOrWhiteSpace(keyId))
        {
            try { await clients.KMS().ScheduleKeyDeletionAsync(new ScheduleKeyDeletionRequest { KeyId = keyId, PendingWindowInDays = 7 }); } catch { }
        }
    }

    private static string RequireKeyId(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }
}
