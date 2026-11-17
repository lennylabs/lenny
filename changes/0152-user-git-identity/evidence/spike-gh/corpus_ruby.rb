require 'json'; require 'graphql'
if ARGV[0] == 'c' then require 'graphql/c_parser'; GraphQL.default_parser = GraphQL::CParser end
c = JSON.parse(File.read('/o/run/corpus.json', encoding: 'UTF-8')); out = {}
def top(sels, frs, seen)
  sels.flat_map do |s|
    case s
    when GraphQL::Language::Nodes::Field then [s.name]
    when GraphQL::Language::Nodes::InlineFragment then top(s.selections, frs, seen)
    when GraphQL::Language::Nodes::FragmentSpread
      if frs[s.name] && !seen[s.name] then seen[s.name]=true; top(frs[s.name].selections, frs, seen) else [] end
    else [] end
  end
end
c.each do |id, doc|
  begin
    d = GraphQL.parse(doc); frs = {}
    d.definitions.each { |n| frs[n.name]=n if n.is_a?(GraphQL::Language::Nodes::FragmentDefinition) }
    ops = d.definitions.select { |n| n.is_a?(GraphQL::Language::Nodes::OperationDefinition) }.map { |o| {type: (o.operation_type||'query'), name: o.name.to_s, fields: top(o.selections, frs, {})} }
    other = d.definitions.reject { |n| n.is_a?(GraphQL::Language::Nodes::OperationDefinition)||n.is_a?(GraphQL::Language::Nodes::FragmentDefinition) }.map { |n| n.class.name.split('::').last }
    out[id] = other.empty? ? {ops: ops} : {ops: ops, other: other}
  rescue StandardError => e
    out[id] = {err: e.class.name + ': ' + e.message.split("\n")[0].to_s[0,120]}
  end
end
puts JSON.generate(out)
