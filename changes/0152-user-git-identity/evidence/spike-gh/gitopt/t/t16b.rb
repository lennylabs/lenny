# t16b.rb: a generic Rails router. Nothing here says GitHub uses it for REST.
require 'rack'; require 'rack/mock'; require 'action_dispatch'; require 'action_controller'
class C < ActionController::Metal; def m; self.response_body='merge'; end; def r; self.response_body='reviews'; end; def g; self.response_body='graphql'; end; end
set=ActionDispatch::Routing::RouteSet.new
set.draw { put '/repos/:o/:r/pulls/:n/merge', to: C.action(:m); post '/repos/:o/:r/pulls/:n/reviews', to: C.action(:r); post '/graphql', to: C.action(:g) }
puts "actionpack #{ActionPack.version}"
[['PUT','/repos/a/b/pulls/1/merge'],['PUT','/repos/a/b/pulls/1/m%65rge'],['PUT','/repos/a/b/pulls/1/merge/'],['PUT','/repos/a/b/pulls/1/merge.json'],['PUT','/repos/a/b/pulls/1/MERGE'],['PUT','/repos/a/b//pulls/1/merge'],['POST','/repos/a/b/pulls/1/r%65views'],['POST','/repos/a/b/pulls/1/reviews/'],['POST','/repos/a/b/pulls/1/reviews.json'],['POST','/graphq%6C'],['POST','/graphql/'],['POST','/graphql.json']].each do |v,p|
  e=Rack::MockRequest.env_for('',method: v); e['PATH_INFO']=p
  r=begin; set.call(e); rescue => x; [500,{},[x.class.to_s]]; end
  s=''; r[2].each{|y| s+=y.to_s}
  puts "rails %-5s %-40s %s %s" % [v,p,r[0],s[0,40]]
end
