using System.Globalization;

namespace OvercastCompat.Scenario;

/// <summary>
/// Paths (compat/model/README.md § Paths): <c>$</c> is the response,
/// <c>.Name</c> selects a structure member or map key, <c>[n]</c> selects a
/// list element. Nothing else — no wildcards, filters, quoting or recursive
/// descent.
/// </summary>
/// <remarks>
/// A path is walked over the <em>document</em> form of a response
/// (<see cref="Documents"/>), not over the SDK object, so member names are the
/// modeled names every backend writes and a null property is absence rather
/// than null.
/// </remarks>
internal static class Paths
{
    /// <summary>One step of a path: a member name or a list index.</summary>
    internal readonly record struct Segment(string Member, int Index)
    {
        public bool IsIndex => Index >= 0;
    }

    /// <summary>
    /// Splits a path into its segments, rejecting anything the IR's path
    /// grammar does not admit — so a malformed path fails the step rather than
    /// silently resolving to nothing. The two are very different bugs.
    /// </summary>
    /// <exception cref="ScenarioPathException">If the path is malformed.</exception>
    public static IReadOnlyList<Segment> Parse(string path)
    {
        if (path.Length == 0 || path[0] != '$')
        {
            throw new ScenarioPathException($"path \"{path}\" does not start with $");
        }
        var segments = new List<Segment>();
        var rest = path.AsSpan(1);
        while (rest.Length > 0)
        {
            if (rest[0] == '.')
            {
                rest = rest[1..];
                var end = rest.IndexOfAny('.', '[');
                if (end < 0)
                {
                    end = rest.Length;
                }
                if (end == 0)
                {
                    throw new ScenarioPathException($"path \"{path}\" has an empty member name");
                }
                segments.Add(new Segment(rest[..end].ToString(), -1));
                rest = rest[end..];
                continue;
            }
            if (rest[0] == '[')
            {
                var end = rest.IndexOf(']');
                if (end < 0)
                {
                    throw new ScenarioPathException($"path \"{path}\" has an unterminated index");
                }
                var digits = rest[1..end].ToString();
                if (!int.TryParse(digits, NumberStyles.None, CultureInfo.InvariantCulture, out var index))
                {
                    throw new ScenarioPathException($"path \"{path}\" has a non-numeric index \"{digits}\"");
                }
                segments.Add(new Segment("", index));
                rest = rest[(end + 1)..];
                continue;
            }
            throw new ScenarioPathException($"path \"{path}\" has an unexpected character '{rest[0]}'");
        }
        return segments;
    }

    /// <summary>
    /// Walks a path over a document.
    /// </summary>
    /// <returns>
    /// False when any segment is absent — which is what <c>missing</c> tests
    /// for, and what makes an absent list count as empty for listContains and
    /// absent.
    /// </returns>
    /// <exception cref="ScenarioPathException">If the path is malformed.</exception>
    public static bool TryResolve(object? document, string path, out object? value)
    {
        value = null;
        var current = document;
        foreach (var segment in Parse(path))
        {
            if (segment.IsIndex)
            {
                if (current is not List<object?> list || segment.Index >= list.Count)
                {
                    return false;
                }
                current = list[segment.Index];
                continue;
            }
            if (current is not SortedDictionary<string, object?> members)
            {
                return false;
            }
            // The document's keys are .NET property names, which AWSSDK
            // capitalizes; the path's are the modeled member names, which are
            // not always capitalized (SQS's `queueUrls`). Bridging it here
            // rather than by rewriting paths at emit time is deliberate: a
            // failure message has to quote the path the scenario file writes,
            // or field 4 stops matching the other backends'.
            if (!members.TryGetValue(segment.Member, out current)
                && !members.TryGetValue(PropertyName(segment.Member), out current))
            {
                return false;
            }
        }
        value = current;
        return true;
    }

    /// <summary>
    /// The .NET property AWSSDK generates for a modeled member: the member name
    /// with its first letter capitalized.
    /// </summary>
    /// <remarks>
    /// Almost every AWS member is already PascalCase, but not all — SQS models
    /// ListDeadLetterSourceQueues' page as <c>queueUrls</c> and CreateQueue's
    /// tags as <c>tags</c>, and the .NET SDK spells both with a capital. Only
    /// the capitalization is reproduced: a member that needed more would not
    /// resolve and would fail the check that names it rather than passing
    /// wrongly.
    /// </remarks>
    public static string PropertyName(string member) =>
        member.Length == 0 || !char.IsLower(member[0])
            ? member
            : char.ToUpperInvariant(member[0]) + member[1..];
}

/// <summary>A path outside the IR's grammar.</summary>
internal sealed class ScenarioPathException(string message) : Exception(message);
