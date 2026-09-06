namespace OvercastCompat.Harness;

public sealed class TestContext
{
    private readonly Dictionary<string, object?> _state = new(StringComparer.Ordinal);
    private readonly object _gate = new();

    public TestContext(string endpoint, string region, string runId)
    {
        Endpoint = endpoint;
        Region = region;
        RunId = runId;
    }

    public string Endpoint { get; }
    public string Region { get; }
    public string RunId { get; }

    public void Set(string key, object? value)
    {
        lock (_gate)
        {
            _state[key] = value;
        }
    }

    public T? Get<T>(string key)
    {
        lock (_gate)
        {
            return _state.TryGetValue(key, out var value) && value is T typed ? typed : default;
        }
    }

    /// <summary>
    /// Returns the value stored under <paramref name="key"/>, or stores and
    /// returns what <paramref name="create"/> produces when there is none.
    /// </summary>
    /// <remarks>
    /// The lookup and the store happen under one lock, which a Get-then-Set
    /// pair does not: the tests of a parallel group share one TestContext, and
    /// two of them racing to create the same lazily built value would each get
    /// a different one.
    /// </remarks>
    public object LoadOrStore(string key, Func<object> create)
    {
        lock (_gate)
        {
            if (_state.TryGetValue(key, out var existing) && existing is not null)
            {
                return existing;
            }
            var created = create();
            _state[key] = created;
            return created;
        }
    }

    public string? GetString(string key) => Get<string>(key);

    public void Log(string message) => Console.Error.WriteLine($"[dotnet-sdk] {message}");
}
