namespace OvercastCompat.Harness;

public sealed class TestContext
{
    private readonly Dictionary<string, object?> _state = new(StringComparer.Ordinal);

    public TestContext(string endpoint, string region, string runId)
    {
        Endpoint = endpoint;
        Region = region;
        RunId = runId;
    }

    public string Endpoint { get; }
    public string Region { get; }
    public string RunId { get; }

    public void Set(string key, object? value) => _state[key] = value;

    public T? Get<T>(string key)
    {
        return _state.TryGetValue(key, out var value) && value is T typed ? typed : default;
    }

    public string? GetString(string key) => Get<string>(key);

    public void Log(string message) => Console.Error.WriteLine($"[dotnet-sdk] {message}");
}
