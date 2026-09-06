using Amazon.Organizations;
using Amazon.Organizations.Model;
using Amazon.SQS.Model;
using OvercastCompat.Scenario;
using Xunit;

namespace OvercastCompat.Tests;

/// <summary>
/// The two conversions that stand between a typed SDK and the IR's JSON rules:
/// an SDK response into a document, and a path over that document.
/// </summary>
/// <remarks>
/// Every rule the IR states about a response is stated over JSON. The three
/// interpreters hold the parsed response; this suite holds an object of
/// nullables, ConstantClass enums and List&lt;T&gt;s, so the conversion is
/// where this suite either agrees with the others or quietly stops.
/// </remarks>
public sealed class ScenarioDocumentTests
{
    [Fact]
    public void NullIsAbsenceAndNumbersAreJsonNumbers()
    {
        // ListQueues legally omits QueueUrls, and AWSSDK v4 leaves an unset
        // collection null rather than empty.
        Assert.True(Documents.TryConvert(new ListQueuesResponse(), out var listed));
        Assert.False(Paths.TryResolve(listed, "$.QueueUrls", out _));
        Assert.False(Paths.TryResolve(listed, "$.NextToken", out _));

        Assert.True(Documents.TryConvert(
            new ReceiveMessageResponse
            {
                Messages =
                [
                    new Message { MessageId = "m-1", ReceiptHandle = "r-1" },
                ],
            },
            out var received));
        Assert.True(Paths.TryResolve(received, "$.Messages[0].ReceiptHandle", out var handle));
        Assert.Equal("r-1", handle);
        Assert.False(Paths.TryResolve(received, "$.Messages[1].ReceiptHandle", out _));

        // A number is a double on both sides of an equals, whatever width the
        // SDK gave the property.
        Assert.True(Documents.TryConvert(new SendMessageResponse { SequenceNumber = "7" }, out var sent));
        Assert.True(Paths.TryResolve(sent, "$.SequenceNumber", out var sequence));
        Assert.Equal("7", sequence);
    }

    [Fact]
    public void TheSdksOwnBookkeepingIsNotAModeledMember()
    {
        // ResponseMetadata, ContentLength and HttpStatusCode are declared on
        // AmazonWebServiceResponse rather than on the modeled response, and a
        // scenario path can only ever mean a modeled member.
        Assert.True(Documents.TryConvert(new CreateQueueResponse { QueueUrl = "http://q/x" }, out var created));
        foreach (var member in new[] { "$.ResponseMetadata", "$.ContentLength", "$.HttpStatusCode" })
        {
            Assert.False(Paths.TryResolve(created, member, out _), $"{member} reached the document");
        }
        Assert.True(Paths.TryResolve(created, "$.QueueUrl", out _));
    }

    [Fact]
    public void AConstantClassIsTheStringItWraps()
    {
        Assert.True(Documents.TryConvert(
            new DescribePolicyResponse
            {
                Policy = new Policy
                {
                    PolicySummary = new PolicySummary { Id = "p-compat00", Type = PolicyType.SERVICE_CONTROL_POLICY },
                },
            },
            out var described));
        Assert.True(Paths.TryResolve(described, "$.Policy.PolicySummary.Type", out var type));
        Assert.Equal("SERVICE_CONTROL_POLICY", type);
    }

    [Fact]
    public void AMapIsAnObjectAndItsKeysAreItsMembers()
    {
        Assert.True(Documents.TryConvert(
            new GetQueueAttributesResponse
            {
                Attributes = new Dictionary<string, string>
                {
                    ["QueueArn"] = "arn:aws:sqs:us-east-1:000000000000:q",
                    ["VisibilityTimeout"] = "30",
                },
            },
            out var attributes));
        Assert.True(Paths.TryResolve(attributes, "$.Attributes.QueueArn", out var arn));
        Assert.Equal("arn:aws:sqs:us-east-1:000000000000:q", arn);
    }

    [Theory]
    // The modeled member name is what a path writes; the .NET property is what
    // the document holds, and only the capitalization differs.
    [InlineData("queueUrls", "QueueUrls")]
    [InlineData("tags", "Tags")]
    [InlineData("QueueUrl", "QueueUrl")]
    [InlineData("", "")]
    public void APathNamesTheModeledMemberAndResolvesTheDotnetProperty(string member, string property)
    {
        Assert.Equal(property, Paths.PropertyName(member));
    }

    [Fact]
    public void APathOutsideTheGrammarFailsTheStepRatherThanResolvingToNothing()
    {
        foreach (var path in new[] { "QueueUrl", "$.", "$[0", "$['a']" })
        {
            Assert.Throws<ScenarioPathException>(() => Paths.TryResolve(null, path, out _));
        }
    }

    [Fact]
    public void JsonEqualityHasNoCoercion()
    {
        Assert.True(Documents.JsonEqual("30", "30"));
        Assert.False(Documents.JsonEqual("30", 30d));
        Assert.False(Documents.JsonEqual(true, 1d));
        Assert.True(Documents.JsonEqual(
            new SortedDictionary<string, object?>(StringComparer.Ordinal) { ["b"] = 1d, ["a"] = "x" },
            new SortedDictionary<string, object?>(StringComparer.Ordinal) { ["a"] = "x", ["b"] = 1d }));
    }

    [Theory]
    [InlineData(null, true)]
    [InlineData("", true)]
    [InlineData("x", false)]
    [InlineData(0d, false)]
    [InlineData(false, false)]
    public void EmptinessIsTheIRs(object? value, bool empty)
    {
        Assert.Equal(empty, Documents.IsEmpty(value));
    }

    [Fact]
    public void AnEmptyListOrObjectIsEmptyAndANonEmptyOneIsNot()
    {
        Assert.True(Documents.IsEmpty(new List<object?>()));
        Assert.False(Documents.IsEmpty(new List<object?> { "x" }));
        Assert.True(Documents.IsEmpty(new SortedDictionary<string, object?>(StringComparer.Ordinal)));
    }
}
