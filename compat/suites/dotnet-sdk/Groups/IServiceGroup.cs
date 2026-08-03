using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public interface IServiceGroup
{
    /// <summary>
    /// Label for this class's registrations. It is what a duplicate impl key
    /// names, so a collision points at the two files to look in rather than
    /// just the key they disagree about - the class name is the file name,
    /// which is what a reader needs.
    /// </summary>
    string SourceName => GetType().Name;

    IReadOnlyDictionary<string, TestFn> Impls();
    IReadOnlyDictionary<string, SetupFn> Setups();
    IReadOnlyDictionary<string, SetupFn> Teardowns();
}
