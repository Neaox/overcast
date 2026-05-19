using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public interface IServiceGroup
{
    IReadOnlyDictionary<string, TestFn> Impls();
    IReadOnlyDictionary<string, SetupFn> Setups();
    IReadOnlyDictionary<string, SetupFn> Teardowns();
}
