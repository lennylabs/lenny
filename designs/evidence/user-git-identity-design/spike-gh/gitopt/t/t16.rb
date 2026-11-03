# t16.rb: how the routers themselves read the spellings. Grape is what GitLab's Gemfile names for its API; ActionDispatch is a generic Rails router, and nothing here says GitHub uses it for REST.
require 'grape'; require 'rack'; require 'rack/mock'; require 'action_dispatch'; require 'action_controller'
class A < Grape::API
  format :json; prefix :api; version 'v4', using: :path
  resource :projects do
    route_param :id, requirements: /[^\/]+/ do
      post('merge_requests/:iid/approve'){ {hit: 'approve', id: params[:id]} }
      put('merge_requests/:iid/merge'){ {hit: 'merge', id: params[:id]} }
    end
  end
  post('graphql'){ {hit:'graphql'} }
end
puts "grape #{Grape::VERSION} rack #{Rack.release}"
m=Rack::MockRequest.new(A)
[['POST','/api/v4/projects/1/merge_requests/7/approve'],['POST','/api/v4/projects/1/merge_requests/7/appr%6Fve'],['POST','/api/v4/projects/1/merge_requests/7/%61pprove'],['POST','/api/v4/projects/1/merge_requests/7/approve/'],['POST','/api/v4/projects/1/merge_requests/7/approve.json'],['POST','/api/v4/projects/1/merge_requests/7/approve.xml'],['POST','/api/v4/projects/1/merge_requests/7/APPROVE'],['POST','/api/v4/projects/1//merge_requests/7/approve'],['POST','/api/v4/projects/1/merge_requests/7/approve;x=1'],['POST','/api/v4/projects/acme%2Fwidgets/merge_requests/7/approve'],['POST','/api/v4/projects/acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove'],['PUT','/api/v4/projects/1/merge_requests/7/m%65rge'],['PUT','/api/v4/projects/1/merge_requests/7/merge.json'],['PUT','/api/v4/projects/1/merge_requests/7/merge/'],['POST','/api/v4/projects/1/merge_requests/7/merge'],['POST','/api/graphq%6C'],['POST','/api/graphql/'],['POST','/api/graphql.json']].each do |v,p|
  e=Rack::MockRequest.env_for('',method: v); e['PATH_INFO']=p; e['REQUEST_PATH']=p; e['REQUEST_URI']=p
  r=A.call(e); b=r[2]; s=''; b.each{|x| s+=x}
  puts "grape %-5s %-62s %s %s" % [v,p,r[0],s[0,60]]
end
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
