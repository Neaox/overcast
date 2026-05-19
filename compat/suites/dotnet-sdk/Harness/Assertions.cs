namespace OvercastCompat.Harness;

public static class Assertions
{
    public static void True(bool condition, string message)
    {
        if (!condition)
        {
            throw new InvalidOperationException(message);
        }
    }

    public static void False(bool condition, string message)
    {
        if (condition)
        {
            throw new InvalidOperationException(message);
        }
    }

    public static void NotNull(object? value, string name)
    {
        if (value is null)
        {
            throw new InvalidOperationException($"expected non-null: {name}");
        }
    }

    public static void NotBlank(string? value, string name)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            throw new InvalidOperationException($"expected non-blank string for: {name}");
        }
    }

    public static void Equal<T>(T expected, T actual, string message)
    {
        if (!EqualityComparer<T>.Default.Equals(expected, actual))
        {
            throw new InvalidOperationException($"{message}: expected <{expected}> but was <{actual}>");
        }
    }

    public static void GreaterThan(long threshold, long actual, string message)
    {
        if (actual <= threshold)
        {
            throw new InvalidOperationException($"{message}: expected > {threshold} but was {actual}");
        }
    }

    public static void GreaterThanOrEqual(int threshold, int actual, string message)
    {
        if (actual < threshold)
        {
            throw new InvalidOperationException($"{message}: expected >= {threshold} but was {actual}");
        }
    }
}
